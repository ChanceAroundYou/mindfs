package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) loadExchangeAux(key string, afterSeq int) (map[int][]ExchangeAux, error) {
	entries, err := m.loadExchangeAuxEntries(key, afterSeq)
	if err != nil {
		return nil, err
	}
	items := make(map[int][]ExchangeAux)
	for _, entry := range entries {
		compacted, ok := CompactExchangeAux(entry)
		if !ok {
			continue
		}
		items[compacted.Seq] = append(items[compacted.Seq], compacted)
	}
	return items, nil
}

// loadExchangeAuxWindow 按窗口 seqSet 过滤 aux：仅保留 seq∈seqSet 的条目，避免 aux.line 错位。
func (m *Manager) loadExchangeAuxWindow(key string, seqSet map[int]bool) (map[int][]ExchangeAux, error) {
	if len(seqSet) == 0 {
		return map[int][]ExchangeAux{}, nil
	}
	// 窗口只覆盖尾部若干 seq，而 aux 按写入顺序（seq 升序）追加，故只需从文件尾反向读。
	// 全量读在实测会话上是 11.8MB/281ms 且持 m.mu（阻塞实时流式写入），而窗口真正要的
	// 只有 ~2MB 中的一小段，属纯浪费。
	minSeq := 0
	for seq := range seqSet {
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
	}
	path, err := m.auxPath(key)
	if err != nil {
		return nil, err
	}
	entries, ok := m.readAuxWindowTail(path, minSeq)
	if !ok {
		// 尾部扫描不可用（文件不可 seek / 读取异常）→ 回退全量读，保证正确性。
		entries, err = m.loadExchangeAuxEntries(key, 0)
		if err != nil {
			return nil, err
		}
	}
	items := make(map[int][]ExchangeAux)
	for _, entry := range entries {
		// CUSTOM(G-AT): 窗口读路径走轻压缩（上游这里是 CompactExchangeAux）。
		// 只改这一处：全量 sync / 重锚定在下面另一个调用点，必须保留完整 content。
		// 窗口专用轻压缩：edit/read/execute 的 content 也清空，展开时懒加载。
		compacted, ok := CompactExchangeAuxLight(entry)
		if !ok {
			continue
		}
		if !seqSet[compacted.Seq] {
			continue
		}
		items[compacted.Seq] = append(items[compacted.Seq], compacted)
	}
	return items, nil
}

func (m *Manager) loadExchangeAuxEntries(key string, afterSeq int) ([]ExchangeAux, error) {
	path, err := m.auxPath(key)
	if err != nil {
		return nil, err
	}

	// 增量快路径：仅 afterSeq>0 适用。afterSeq=0 需要全部 aux（如 GetFullToolCall 查找 callID），
	// aux 无内存缓存，必须全量读。增量场景文件未变 → 无新 aux；追加 → 尾部增量读。
	if afterSeq > 0 {
		if cursor, has := m.exchangeCursors[path]; has && cursor.Offset > 0 {
			if info, statErr := m.root.StatMetaFile(path); statErr == nil {
				size := info.Size()
				mtimeNs := info.ModTime().UnixNano()
				switch {
				case size == cursor.Size && mtimeNs == cursor.ModTimeNs:
					return []ExchangeAux{}, nil
				case size > cursor.Offset && mtimeNs != cursor.ModTimeNs:
					if items, ok := m.readAuxTail(path, cursor.Offset, afterSeq); ok {
						m.exchangeCursors[path] = exchangeFileCursor{Offset: size, Size: size, ModTimeNs: mtimeNs, MaxSeq: cursor.MaxSeq}
						return items, nil
					}
				}
			}
		}
	}

	// 全量读（兜底 / afterSeq=0）
	items, err := m.readAuxFile(path, afterSeq)
	if err != nil {
		return nil, err
	}
	if info, statErr := m.root.StatMetaFile(path); statErr == nil {
		m.exchangeCursors[path] = exchangeFileCursor{
			Offset:    info.Size(),
			Size:      info.Size(),
			ModTimeNs: info.ModTime().UnixNano(),
			MaxSeq:    0,
		}
	}
	return items, nil
}

// readAuxWindowTail 从 aux 文件尾部反向分块读取 seq>=minSeq 的条目。
// aux 按写入顺序（seq 升序）追加，因此尾部即最新 seq；窗口只覆盖尾部若干 seq，
// 无需全量读（实测某会话 aux 11.8MB/281ms 且持 m.mu，而窗口要的只是一小段）。
// 返回 ok=false 表示无法可靠完成（不可 seek / 读取异常），由调用方回退全量读。
func (m *Manager) readAuxWindowTail(path string, minSeq int) ([]ExchangeAux, bool) {
	if minSeq <= 0 {
		return nil, false
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		// 文件缺失等价于「无 aux」，不是失败。
		if errors.Is(err, os.ErrNotExist) {
			return []ExchangeAux{}, true
		}
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	if size == 0 {
		return []ExchangeAux{}, true
	}

	const chunk = int64(512 << 10)
	// 即使已看到 seq<minSeq 也再多读两块：aux 的写入顺序不保证跨轮严格单调
	// （子代理等可能在稍后追加较小 seq），留出余量避免漏条目。
	const safetyChunks = 2
	var rawLines [][]byte
	end := size
	safety := 0
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(end-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, false
		}
		if start > 0 {
			// 非文件头：首行可能是半行，丢弃到第一个换行为止。
			idx := bytes.IndexByte(buf, '\n')
			if idx < 0 {
				buf = nil
			} else {
				buf = buf[idx+1:]
			}
		}
		below := false
		part := make([][]byte, 0, 64)
		for _, raw := range bytes.Split(buf, []byte{'\n'}) {
			raw = bytes.TrimSpace(raw)
			if len(raw) == 0 {
				continue
			}
			part = append(part, raw)
			var probe struct {
				Seq int `json:"seq"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Seq > 0 && probe.Seq < minSeq {
				below = true
			}
		}
		rawLines = append(part, rawLines...)
		if below {
			safety++
			if safety >= safetyChunks {
				break
			}
		}
		if start == 0 {
			break
		}
		end = start
	}

	items := make([]ExchangeAux, 0, len(rawLines))
	for _, raw := range rawLines {
		var entry ExchangeAux
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 || entry.Seq < minSeq {
			continue
		}
		items = append(items, entry)
	}
	return items, true
}

func (m *Manager) readAuxFile(path string, afterSeq int) ([]ExchangeAux, error) {
	m.fullAuxReads++
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ExchangeAux{}, nil
		}
		return nil, err
	}
	items := make([]ExchangeAux, 0)
	scanner := jsonlScanner(payload)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ExchangeAux
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			continue
		}
		if afterSeq > 0 && entry.Seq <= afterSeq {
			continue
		}
		items = append(items, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) readAuxTail(path string, offset int64, afterSeq int) ([]ExchangeAux, bool) {
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, false
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxExchangeLineBytes)
	items := make([]ExchangeAux, 0, 8)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ExchangeAux
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			continue
		}
		if afterSeq > 0 && entry.Seq <= afterSeq {
			continue
		}
		items = append(items, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, false
	}
	return items, true
}

func (m *Manager) appendExchangeAux(key string, aux ExchangeAux) error {
	path, err := m.auxPath(key)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(aux)
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
	return nil
}

func (m *Manager) auxPath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(auxFileTpl, key)), nil
}
