package session

import (
	"bytes"
	"encoding/json"
	"sort"
)

// resolveBlocks 按 (seq,pos) 取最高 revision、丢掉未收口的版本，并按 (seq,pos) 升序返回。
//
// seqSet 为 nil 表示不过滤 seq。revision 相同时取**后写入**的那条（文件序靠后者）：
// 块流是 append-only，后写的才是更新的。
func resolveBlocks(blocks []Block, seqSet map[int]bool) []Block {
	if len(blocks) == 0 {
		return []Block{}
	}
	type key struct{ seq, pos int }
	best := make(map[key]Block, len(blocks))
	for _, block := range blocks {
		if block.Seq <= 0 {
			continue
		}
		if seqSet != nil && !seqSet[block.Seq] {
			continue
		}
		// step<0 = 步序未定，读取侧忽略（§3.3）。
		if block.Step < 0 {
			continue
		}
		k := key{block.Seq, block.Pos}
		if prev, exists := best[k]; exists && prev.Revision > block.Revision {
			continue
		}
		best[k] = block
	}
	out := make([]Block, 0, len(best))
	for _, block := range best {
		out = append(out, block)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Pos < out[j].Pos
	})
	return out
}

// parseBlocks 逐行解析。**坏行跳过不报错** —— 块流是 append-only 的，一行写坏了
// 不能让整个会话的正文读不出来（与主日志 parseExchangeLine 的既有取舍一致）。
func parseBlocks(payload []byte) []Block {
	blocks := make([]Block, 0, 16)
	if len(payload) == 0 {
		return blocks
	}
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
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var block Block
		if err := json.Unmarshal(line, &block); err != nil {
			continue
		}
		if block.Seq <= 0 {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// stripJSONFields 逐行删掉给定字段。**行级手术而不是结构体往返**：主日志里可能有
// 别的写入者 / 未来版本加的字段，反序列化成 Exchange 再序列化会把它们丢掉。
// 只有真的删掉了东西才返回 changed=true。
func stripJSONFields(payload []byte, fields ...string) ([]byte, bool) {
	if len(payload) == 0 {
		return payload, false
	}
	out := make([]byte, 0, len(payload))
	changed := false
	rest := payload
	for len(rest) > 0 {
		idx := bytes.IndexByte(rest, '\n')
		var line []byte
		if idx < 0 {
			line = rest
			rest = nil
		} else {
			line = rest[:idx+1]
			rest = rest[idx+1:]
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			out = append(out, line...)
			continue
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			out = append(out, line...)
			continue
		}
		hit := false
		for _, field := range fields {
			raw, exists := decoded[field]
			if !exists {
				continue
			}
			var value string
			// 空值不算「有 native id」：落地前的历史行本来就没有这个字段，
			// 而某些写入者可能写成空串 —— 两者都不需要重写文件。
			if json.Unmarshal(raw, &value) == nil && value == "" {
				continue
			}
			delete(decoded, field)
			hit = true
		}
		if !hit {
			out = append(out, line...)
			continue
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			out = append(out, line...)
			continue
		}
		out = append(out, encoded...)
		out = append(out, '\n')
		changed = true
	}
	if !changed {
		return payload, false
	}
	return out, true
}
