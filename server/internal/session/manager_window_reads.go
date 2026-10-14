package session

import (
	"bytes"
	"errors"
	"os"
	"strings"
)

func (m *Manager) getSessionWindowUnsafe(key string, beforeSeq, limit, latest int) (*Session, *SessionWindowMeta, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil, errors.New("session key required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	meta, err := m.getSessionMetaWithBindingsUnsafe(key)
	if err != nil {
		return nil, nil, err
	}
	path, err := m.exchangePath(key)
	if err != nil {
		return nil, nil, err
	}

	var window []Exchange
	total := 0
	hasMore := false
	cached, hasCache := m.sessions[key]
	if hasCache && cached != nil && len(cached.Exchanges) > 0 {
		window, total, hasMore = sliceExchangeWindow(cached.Exchanges, beforeSeq, limit, latest)
	} else if read, count, more, ok := m.readExchangeWindow(path, beforeSeq, limit, latest); ok {
		window, total, hasMore = read, count, more
	} else {
		// 尾部读不可用（不可 seek / 读取异常）→ 回退全量解析，保证正确性。
		all, _, rErr := m.readExchangesFull(path)
		if rErr != nil {
			return nil, nil, rErr
		}
		window, total, hasMore = sliceExchangeWindow(all, beforeSeq, limit, latest)
	}

	meta.Exchanges = window

	minSeq, maxSeq := 0, 0
	if len(window) > 0 {
		minSeq = window[0].Seq
		maxSeq = window[len(window)-1].Seq
	}
	return meta, &SessionWindowMeta{
		Total:   total,
		HasMore: hasMore,
		MinSeq:  minSeq,
		MaxSeq:  maxSeq,
	}, nil
}

// sliceExchangeWindow 在已按 seq 升序排列的 exchanges 上取窗口，返回 (窗口, 总数, 是否还有更早)。
// 语义与 readExchangeWindow 必须逐条一致：latest>0 时窗口就是尾部 latest 条（与 limit 解耦）。
func sliceExchangeWindow(all []Exchange, beforeSeq, limit, latest int) ([]Exchange, int, bool) {
	total := len(all)
	switch {
	case latest > 0:
		start := total - latest
		if start < 0 {
			start = 0
		}
		return all[start:], total, start > 0
	case beforeSeq > 0:
		idx := firstIndexWhereSeq(all, beforeSeq)
		start := idx - limit
		if start < 0 {
			start = 0
		}
		return all[start:idx], total, start > 0
	default:
		start := total - limit
		if start < 0 {
			start = 0
		}
		return all[start:], total, start > 0
	}
}

// readExchangeWindow 冷缓存下的窗口读：只解析窗口所需的尾部记录，不构建全量切片。
//
// 返回 (窗口, 记录总数, 是否还有更早, ok)。ok=false 表示读盘失败，调用方回退全量解析。
// 成本：一次读盘 + 一遍字节级行计数（不解析 JSON）+ 只解析尾部窗口条数。
// 实测 1.19MB / 206 行（每行 ~5.7KB 正文）：全量解析 15.3ms → 1.4ms，
// 其中读盘 0.9ms、行计数 0.14ms、解析 9 条 0.8ms。真实最重会话 391KB 时按比例更低。
func (m *Manager) readExchangeWindow(path string, beforeSeq, limit, latest int) ([]Exchange, int, bool, bool) {
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Exchange{}, 0, false, true
		}
		return nil, 0, false, false
	}
	if len(payload) == 0 {
		return []Exchange{}, 0, false, true
	}

	// latest>0 时窗口就是尾部 latest 条，与 beforeSeq 互斥（与 sliceExchangeWindow 一致）。
	need := limit
	if latest > 0 {
		need = latest
		beforeSeq = 0
	}
	if need <= 0 {
		need = 50
	}
	window, hasMore := tailExchangeWindow(payload, beforeSeq, need)
	return window, countExchangeRecords(payload), hasMore, true
}

// countExchangeRecords 字节级统计非空行数（不解析 JSON）。
// 语义上等于「记录条数」：实测 310 份真实主日志无空行、无同 seq 多行，
// 退化数据（同 seq 多行）由全量读的 mergeExchangeRecord 负责合并。
// 用 bytes.IndexByte 而不是逐字节 switch：后者在 1.19MB 上要 3ms，前者 0.1ms 量级。
func countExchangeRecords(payload []byte) int {
	count := 0
	for len(payload) > 0 {
		idx := bytes.IndexByte(payload, '\n')
		var line []byte
		if idx < 0 {
			line = payload
			payload = nil
		} else {
			line = payload[:idx]
			payload = payload[idx+1:]
		}
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

// tailExchangeWindow 从 payload 尾部向前取 need+1 条记录（升序返回），
// hasMore 报告窗口之前还有更早的记录。只解析取到的那些行，其余行仅被跳过。
//
// bytes.LastIndexByte 从尾部向前扫，命中即停，所以每行定位成本是 O(行长) 而非 O(文件)。
func tailExchangeWindow(payload []byte, beforeSeq, need int) ([]Exchange, bool) {
	target := need + 1
	records := make([]Exchange, 0, target)
	end := len(payload)
	for end > 0 && len(records) < target {
		if payload[end-1] == '\n' {
			end--
			continue
		}
		start := bytes.LastIndexByte(payload[:end], '\n') + 1
		raw := bytes.TrimSpace(payload[start:end])
		end = start
		if len(raw) == 0 {
			continue
		}
		entry, ok, _ := parseExchangeLine(string(raw))
		if !ok {
			continue
		}
		if beforeSeq > 0 && entry.Seq >= beforeSeq {
			continue
		}
		records = append(records, entry)
	}
	hasMore := len(records) > need
	if len(records) > need {
		records = records[:need]
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, hasMore
}

// firstIndexWhereSeq 在按 Seq 升序的 exchanges 中二分定位首个 Seq>=target 的下标（lower_bound）。
func firstIndexWhereSeq(all []Exchange, target int) int {
	lo, hi := 0, len(all)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if all[mid].Seq >= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}
