package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// GetBlocksWindow 返回窗口内 seq 的块，已解析 revision 并按 (seq,pos) 升序。
//
// 这是渲染读路径，两条硬约束：
//  1. **O(窗口)**。块流是窗口回包的载荷来源，而窗口读持 m.mu（每 root 一把），
//     全量读会卡住整个 root。实测单会话一轮可达 83 步 / 974 条消息，块流是 MB 级。
//     做法与 readAuxWindowTail 同构：从尾部反向分块读，直到看见一条 seq < minSeq
//     的完整行（或读到文件头）。
//  2. **解析 revision 与哨兵**。同 (seq,pos) 取最高 revision（D8）；step<0 的版本是
//     「步序未定」的中间态，读取侧忽略（§3.3）。反向读天然先遇到最高 revision。
func (m *Manager) GetBlocksWindow(ctx context.Context, key string, seqSet map[int]bool) ([]Block, error) {
	if len(seqSet) == 0 {
		return []Block{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	minSeq := 0
	for seq := range seqSet {
		if seq <= 0 {
			continue
		}
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
	}
	if minSeq == 0 {
		return []Block{}, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.blocksPath(key)
	if err != nil {
		return nil, err
	}
	raw, ok := m.readBlocksWindowTail(path, minSeq)
	if !ok {
		// 反向读无法可靠完成（不可 seek / 读异常）时回退全量读。宁可慢也不能漏正文。
		all, err := m.readBlocksAll(key)
		if err != nil {
			return nil, err
		}
		raw = all
	}
	return resolveBlocks(raw, seqSet), nil
}

// readBlocksWindowTail 读回 seq >= minSeq 的原始块行。ok=false 表示无法可靠完成，
// 由调用方回退全量读。
//
// 两阶段，刻意不把「反向分块读」的结果直接当内容：
//
//	阶段一（定位）：从尾部反向分块，只解析每块**内部**的完整行（跳过跨块的首尾残片），
//	  找到第一个出现 seq < minSeq 的块起点。
//	阶段二（取内容）：把该起点**对齐到行边界**，然后一次正向读到文件尾。
//
// 为什么不直接收集反向读到的行：反向分块读的经典坑是「跨块的长行被静默丢掉」。
// 每块的首行是半行（前半段在更低偏移处），丢掉它需要「下一块会把完整行读全」这个前提，
// 而单行大于 chunk 时该前提不成立 —— 实测主日志单行最大 308KB，块流里超长 reasoning
// 常见（os 实测 119714 / 103467 字符）。先对齐再正向读就不存在残片拼接问题：
// 从行边界读到文件尾，每一行都是完整的。
//
// 代价是同一段字节被读两遍（定位一遍、取内容一遍），换来的是「不可能丢正文」。
// 定位粒度是 chunk，所以最小读量是 512KB —— 与 readAuxWindowTail 同量级。
func (m *Manager) readBlocksWindowTail(path string, minSeq int) ([]Block, bool) {
	if minSeq <= 0 {
		return nil, false
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		// 文件缺失等价于「没有块」，不是失败 —— 落地前的历史会话都是这样。
		if errors.Is(err, os.ErrNotExist) {
			return []Block{}, true
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
		return []Block{}, true
	}

	const chunk = int64(512 << 10)
	stop := int64(-1)
	end := size
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(end-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, false
		}
		if chunkHasSeqBelow(buf, start > 0, end < size, minSeq) {
			stop = start
			break
		}
		if start == 0 {
			stop = 0
			break
		}
		end = start
	}
	if stop < 0 {
		stop = 0
	}

	alignedStart, ok := alignLineStart(file, stop, size)
	if !ok {
		return nil, false
	}
	payload := make([]byte, int(size-alignedStart))
	if _, err := file.ReadAt(payload, alignedStart); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	return parseBlocks(payload), true
}

// chunkHasSeqBelow 只看 buf 里**内部**的完整行（skipFirst/skipLast 表示首尾是跨块残片），
// 判断是否存在 seq < minSeq 的行。坏行不算「低于」：它可能本来属于窗口内的 seq，
// 过早停止会让窗口内的正文少一块。
func chunkHasSeqBelow(buf []byte, skipFirst, skipLast bool, minSeq int) bool {
	parts := bytes.Split(buf, []byte{'\n'})
	if skipFirst && len(parts) > 0 {
		parts = parts[1:]
	}
	if skipLast && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	for _, rawLine := range parts {
		rawLine = bytes.TrimSpace(rawLine)
		if len(rawLine) == 0 {
			continue
		}
		var probe struct {
			Seq int `json:"seq"`
		}
		if json.Unmarshal(rawLine, &probe) == nil && probe.Seq > 0 && probe.Seq < minSeq {
			return true
		}
	}
	return false
}

// alignLineStart 返回 offset 处或其之前最近的行边界（即「某个 '\n' 之后」的偏移）。
// offset <= 0 时返回 0。找不到 '\n'（整个前缀是一行）时返回 0 —— 那也是行首。
// 用几何步长向前回溯，避免为超长行退化成逐字节扫描。
func alignLineStart(file io.ReaderAt, offset, size int64) (int64, bool) {
	if offset <= 0 {
		return 0, true
	}
	if offset > size {
		offset = size
	}
	const step = int64(64 << 10)
	pos := offset
	for pos > 0 {
		start := pos - step
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(pos-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, false
		}
		if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
			return start + int64(idx) + 1, true
		}
		pos = start
	}
	return 0, true
}
