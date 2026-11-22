package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// 块流的流式读取（CUSTOM(G-PY)）。
//
// 长会话实测单文件 70 MB / 4.5 万行。发送路径每轮的 CountBlocksAfterSeq、
// 崩溃恢复时的封口都要读一遍块流；原先 ReadMetaFile 全量进内存再
// strings.Split，等于同时付一份 70 MB 副本和 4.5 万个行字符串（实测 685 ms /
// 157 MB 分配），而且行长没有上限（线上单行最大 289 KB）。这里统一改成
// 「复用缓冲、逐行回调」：峰值内存只跟最长一行走。

// blockLineBuffer 是行缓冲的初始大小。行超过它时按需扩容，不会截断。
const blockLineBuffer = 256 << 10

// forEachBlockLine 逐行回调块流。行尾换行符会被去掉，空行不回调。
//
// **没有换行结尾的末行不回调**：AppendBlocks 每块都写 `payload + '\n'`，
// 所以「读到 EOF 却不是以换行收尾」只能是崩溃留下的半行。不丢的话，乱码
// 半行里的 `{"seq":4,` 会被前缀解析认成一个合法 seq —— 计数虚高一行，界面上
// 也会多出一条读不出来的块。旧实现靠整行 json.Unmarshal 失败顺带跳过它，
// 流式路径没有这个副作用，必须显式判。
func forEachBlockLine(r io.Reader, fn func(line []byte) bool) error {
	if r == nil || fn == nil {
		return nil
	}
	reader := bufio.NewReaderSize(r, blockLineBuffer)
	var longLine []byte
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// 超长行：攒到换行或 EOF。longLine 跨行复用，避免按行分配。
			longLine = append(longLine[:0], line...)
			for err == bufio.ErrBufferFull {
				line, err = reader.ReadSlice('\n')
				longLine = append(longLine, line...)
			}
			line = longLine
		}
		if n := len(line); n > 0 && line[n-1] == '\n' {
			if n--; n > 0 && !fn(line[:n]) {
				return nil
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// blockSeqFromLine 只取一行的 seq。AppendBlocks 用结构体序列化，seq 恒为第一个
// 字段，所以命中前缀时不用解码整行（块流里 70 MB 的正文与工具输出都不碰）；
// 顺序不同或格式不认识时回退到 json.Unmarshal，行为与旧实现一致。
//
// 前缀路径额外要求行尾是 `}`：块行是 json.Marshal 出来的紧凑对象，不可能以
// 别的字节收尾。没有这道校验时，崩溃留下的半行只要前几个字节对得上
// （`{"seq":4,`）就会被认成一个合法块，计数虚高、界面上多一条读不出来的块。
func blockSeqFromLine(line []byte) (int, bool) {
	const prefix = `{"seq":`
	if len(line) > len(prefix) && line[len(line)-1] == '}' && bytes.HasPrefix(line, []byte(prefix)) {
		i := len(prefix)
		negative := false
		if line[i] == '-' {
			negative = true
			i++
		}
		start := i
		n := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			n = n*10 + int(line[i]-'0')
			i++
		}
		if i > start && i < len(line) && (line[i] == ',' || line[i] == '}') {
			if negative {
				n = -n
			}
			return n, true
		}
	}
	var head struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return 0, false
	}
	return head.Seq, true
}
