package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// GetCommandShell 推断命令会话的 shell 标签。
//
// 只要「最新状态」：判据是最后一条 source=userShell、phase=final 的 commandExecution，
// 它总是写在 aux 尾部。全量读在实测会话上可达 11.8MB/281ms 且持 m.mu，而列表页对每个
// 命令会话各读一次（fillCommandShells）—— 改成尾部有界读，命中即停。
func (m *Manager) GetCommandShell(_ context.Context, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("session key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	path, err := m.auxPath(key)
	if err != nil {
		return "", err
	}
	info, err := m.root.StatMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	size := info.Size()
	if size == 0 {
		return "", nil
	}

	span := int64(512 << 10)
	for {
		entries, ok := m.readAuxTailEntries(path, size, span)
		if !ok {
			// 尾部读不可用（不可 seek / 读取异常）→ 回退全量读，保证正确性。
			all, err := m.loadExchangeAuxEntries(key, 0)
			if err != nil {
				return "", err
			}
			return InferCommandShellFromAux(groupAuxBySeq(all)), nil
		}
		if shell := InferCommandShellFromAux(groupAuxBySeq(entries)); shell != "" {
			return shell, nil
		}
		if span >= size {
			return "", nil
		}
		span *= 4
	}
}

// groupAuxBySeq 把 aux 条目按 seq 分组（保持写入顺序）。
func groupAuxBySeq(entries []ExchangeAux) map[int][]ExchangeAux {
	items := make(map[int][]ExchangeAux, len(entries))
	for _, entry := range entries {
		items[entry.Seq] = append(items[entry.Seq], entry)
	}
	return items
}

// readAuxTailEntries 读取 aux 文件尾部至多 span 字节的条目（升序返回）。
// 起点对齐到换行边界，因此不会在边界上截断记录。ok=false 表示读取异常。
func (m *Manager) readAuxTailEntries(path string, size, span int64) ([]ExchangeAux, bool) {
	start := size - span
	if start < 0 {
		start = 0
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	buf := make([]byte, size-start)
	if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	if start > 0 {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			buf = nil
		} else {
			buf = buf[idx+1:]
		}
	}
	entries := make([]ExchangeAux, 0, 64)
	for len(buf) > 0 {
		idx := bytes.IndexByte(buf, '\n')
		var line []byte
		if idx < 0 {
			line = buf
			buf = nil
		} else {
			line = buf[:idx]
			buf = buf[idx+1:]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry ExchangeAux
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, true
}
