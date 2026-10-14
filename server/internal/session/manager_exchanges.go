package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
)

// exchangeFileCursor 记录 exchanges/aux JSONL 文件的读取游标，用于增量读取：
// 文件 (size, mtime) 未变 → 复用内存缓存；追加 → 从 Offset 续读尾部，避免每次全量读盘+反序列化。
type exchangeFileCursor struct {
	Offset    int64
	Size      int64
	ModTimeNs int64
	MaxSeq    int
}

func (m *Manager) loadExchanges(key string, afterSeq int) ([]Exchange, int, error) {
	path, err := m.exchangePath(key)
	if err != nil {
		return nil, 0, err
	}

	// 增量快路径：文件与游标一致 → 直接过滤内存缓存；追加 → 尾部增量读，避免全量读盘+反序列化。
	if cached, ok := m.sessions[key]; ok && cached != nil && len(cached.Exchanges) > 0 {
		if cursor, has := m.exchangeCursors[path]; has && cursor.Offset > 0 {
			if info, statErr := m.root.StatMetaFile(path); statErr == nil {
				size := info.Size()
				mtimeNs := info.ModTime().UnixNano()
				switch {
				case size == cursor.Size && mtimeNs == cursor.ModTimeNs:
					return filterExchanges(cached.Exchanges, afterSeq), cursor.MaxSeq, nil
				case size > cursor.Offset && mtimeNs != cursor.ModTimeNs:
					// 写路径（AddExchangeForAgent 等）会同步更新缓存但 cursor 不前进，
					// 以缓存 max seq 为基准去重，避免重复追加已读条目。
					baseSeq := cursor.MaxSeq
					if cachedMax := maxExchangeSeq(cached.Exchanges); cachedMax > baseSeq {
						baseSeq = cachedMax
					}
					added, maxSeq, ok := m.readExchangeTail(path, cursor.Offset, baseSeq)
					if ok {
						// 尾部条目按 seq 合并（同 seq 覆盖、尾部内部去重），
						// 不能盲目 append —— 否则同 seq 的后续版本会变成重复行。
						cached.Exchanges = mergeExchangeRecords(cached.Exchanges, added, path)
						m.exchangeCursors[path] = exchangeFileCursor{Offset: size, Size: size, ModTimeNs: mtimeNs, MaxSeq: maxSeq}
						return filterExchanges(cached.Exchanges, afterSeq), maxSeq, nil
					}
				}
			}
		}
	}

	// 全量读（兜底：首读/文件被重写/尾部解析失败）
	exchanges, total, err := m.readExchangesFull(path)
	if err != nil {
		return nil, 0, err
	}
	if info, statErr := m.root.StatMetaFile(path); statErr == nil {
		m.exchangeCursors[path] = exchangeFileCursor{
			Offset:    info.Size(),
			Size:      info.Size(),
			ModTimeNs: info.ModTime().UnixNano(),
			MaxSeq:    total,
		}
	}
	if cached, ok := m.sessions[key]; ok && cached != nil {
		cached.Exchanges = exchanges
	}
	return filterExchanges(exchanges, afterSeq), total, nil
}

// parseExchangeLine 解析一行 exchange。
// 行首可能被 NUL 零填充（并发/外部写入留下的空洞：实测 2026-09-14 AIS/docs 会话有
// ~4100 字节 NUL + 一段完整 JSON），剥掉前缀后仍可完整还原，故先按原样解析，失败再剥。
// 返回 (entry, 是否解析成功, 是否修复了 NUL 前缀)。
func parseExchangeLine(line string) (Exchange, bool, bool) {
	var entry Exchange
	if err := json.Unmarshal([]byte(line), &entry); err == nil {
		return entry, true, false
	}
	if trimmed := strings.TrimLeft(line, "\x00"); trimmed != line {
		if err := json.Unmarshal([]byte(trimmed), &entry); err == nil {
			return entry, true, true
		}
	}
	return Exchange{}, false, false
}

// mergeExchangeRecord 把一条解析出来的记录按 seq 合并进 exchanges（就地替换或追加）。
//
// 规则（全量读与尾部增量读共用，两条路径的判重语义必须一致）：
//   - 新 seq：追加。顺序沿用文件顺序 —— 与 readExchangesFull 的既有假设一致。
//   - 已有 seq：就地替换（保留原位置与 seq，aux 引用与游标才不失效）。
//     已完成的正文优先于占位空正文；否则后者胜。
//   - 只有「两行都有非空正文且不同」才算真冲突并告警。字段补全（同 seq 的后续版本
//     带来更多信息）是增量更新，不是冲突 —— S2a 起由 revision 承担同一职责。
func mergeExchangeRecord(exchanges []Exchange, indexBySeq map[int]int, entry Exchange, path string) []Exchange {
	index, exists := indexBySeq[entry.Seq]
	if !exists {
		indexBySeq[entry.Seq] = len(exchanges)
		return append(exchanges, entry)
	}
	previous := exchanges[index]
	// LegacyContent：同 seq 的重复行只在**老数据**（S2d-2 之前写的）里还有正文，
	// 新写入的行这个字段恒空，所以这段判据对它们是恒等的 no-op。
	if strings.TrimSpace(previous.LegacyContent) != "" && strings.TrimSpace(entry.LegacyContent) == "" {
		return exchanges
	}
	if strings.TrimSpace(previous.LegacyContent) != "" && previous.LegacyContent != entry.LegacyContent {
		log.Printf("[session/load] conflicting duplicate seq session=%s seq=%d; using last non-empty record", path, entry.Seq)
	}
	exchanges[index] = entry
	return exchanges
}

// mergeExchangeRecords 是 mergeExchangeRecord 的批量入口（尾部增量读用）。
func mergeExchangeRecords(base, incoming []Exchange, path string) []Exchange {
	if len(incoming) == 0 {
		return base
	}
	indexBySeq := make(map[int]int, len(base)+len(incoming))
	for i, entry := range base {
		if _, exists := indexBySeq[entry.Seq]; !exists {
			indexBySeq[entry.Seq] = i
		}
	}
	for _, entry := range incoming {
		base = mergeExchangeRecord(base, indexBySeq, entry, path)
	}
	return base
}

func (m *Manager) readExchangesFull(path string) ([]Exchange, int, error) {
	m.fullExchangeReads++
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Exchange{}, 0, nil
		}
		return nil, 0, err
	}
	exchanges := make([]Exchange, 0)
	indexBySeq := make(map[int]int)
	total := 0
	skipped, repaired := 0, 0
	scanner := jsonlScanner(payload)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, ok, fixed := parseExchangeLine(line)
		if !ok {
			skipped++
			continue
		}
		if fixed {
			repaired++
		}
		if entry.Seq <= 0 {
			entry.Seq = total + 1
		}
		if entry.Seq > total {
			total = entry.Seq
		}
		exchanges = mergeExchangeRecord(exchanges, indexBySeq, entry, path)
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if skipped > 0 || repaired > 0 {
		log.Printf("[session/store] parse.damaged path=%s skipped=%d nul_repaired=%d", path, skipped, repaired)
	}
	return exchanges, total, nil
}

// readExchangeTail 从 offset 续读追加的 JSONL 行，返回解析出的全部条目与新的 maxSeq。
//
// 刻意**不**跳过 seq <= baseSeq 的条目：主日志允许同一 seq 有后续版本（占位行补全、
// 工具结果晚到、S2a 起的 revision 覆盖），按 seq 盲跳会让这些更新对增量快路径永久不可见
// （O4：这条正是 loadExchanges 的最热读路径）。去重与合并由调用方的 mergeExchangeRecords
// 统一处理，两条读路径（全量 / 增量）语义因此一致。
// 解析失败/读取错误返回 ok=false，由调用方回退全量读。
func (m *Manager) readExchangeTail(path string, offset int64, baseSeq int) ([]Exchange, int, bool) {
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		return nil, baseSeq, false
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, baseSeq, false
	}
	total := baseSeq
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxExchangeLineBytes)
	added := make([]Exchange, 0, 8)
	skipped, repaired := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, ok, fixed := parseExchangeLine(line)
		if !ok {
			skipped++
			continue
		}
		if fixed {
			repaired++
		}
		if entry.Seq <= 0 {
			entry.Seq = total + 1
		}
		if entry.Seq > total {
			total = entry.Seq
		}
		added = append(added, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, baseSeq, false
	}
	if skipped > 0 || repaired > 0 {
		log.Printf("[session/store] parse.damaged path=%s skipped=%d nul_repaired=%d tail=true", path, skipped, repaired)
	}
	return added, total, true
}

// MaxExchangeSeq 返回序列里的最大 seq（空序列返回 0）。写入路径用它分配下一个 seq，
// 调用方（如乐观合并的 seq 预测）也必须用它，不能再用 len(exchanges)+1。
func MaxExchangeSeq(exchanges []Exchange) int {
	return maxExchangeSeq(exchanges)
}

func maxExchangeSeq(exchanges []Exchange) int {
	max := 0
	for _, e := range exchanges {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max
}

// TurnIDForSeq 返回某一行的 mindfs_id（找不到返回空串）。
//
// 与 MaxExchangeSeq 对称的读法：块流要带 mindfs_id 才能挂回轮次，而 `max+1` 分配下
// 「刚写下的那一行」的 seq 只有调用方算得出、mindfs_id 却存在行里。导入路径就是这样
// —— 它拿到 seq 之后还得回过头来问这一行的身份。
func TurnIDForSeq(exchanges []Exchange, seq int) string {
	for _, e := range exchanges {
		if e.Seq == seq {
			return e.MindFSID
		}
	}
	return ""
}

func filterExchanges(all []Exchange, afterSeq int) []Exchange {
	if afterSeq <= 0 {
		out := make([]Exchange, len(all))
		copy(out, all)
		return out
	}
	out := make([]Exchange, 0, len(all))
	for _, e := range all {
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out
}

func (m *Manager) appendExchange(key string, exchange Exchange) error {
	path, err := m.exchangePath(key)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(exchange)
	if err != nil {
		return err
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	// 写路径推进读盘游标：否则缓存新鲜度校验会永远判定为陈旧，每次读都多跑一次尾部读。
	m.advanceExchangeCursorUnsafe(path, file, exchange.Seq)
	return nil
}

// advanceExchangeCursorUnsafe 把读盘游标推到刚写入的位置（仅在持 m.mu 时调用）。
func (m *Manager) advanceExchangeCursorUnsafe(path string, file *os.File, seq int) {
	info, err := file.Stat()
	if err != nil {
		return
	}
	maxSeq := seq
	if cursor, ok := m.exchangeCursors[path]; ok && cursor.MaxSeq > maxSeq {
		maxSeq = cursor.MaxSeq
	}
	m.exchangeCursors[path] = exchangeFileCursor{
		Offset:    info.Size(),
		Size:      info.Size(),
		ModTimeNs: info.ModTime().UnixNano(),
		MaxSeq:    maxSeq,
	}
}
