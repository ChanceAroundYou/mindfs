package session

import (
	"context"
	"sort"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

// provisional 块走的是既有块流的「步序未定 + revision 覆盖」机制：
// 写入时 Step=-1，读取侧按 GetBlocksWindow 的规矩忽略；崩溃恢复时再追加
// 同 (seq,pos)、revision+1、Step>=0 的封口版本。这样流式正文在 kill -9
// 之前就已经落盘，又不会在正常回合里重复渲染。
func (m *Manager) AppendProvisionalText(ctx context.Context, key string, seq int, turnID string, pos int, text string) error {
	if blankBlockText(text) {
		return nil
	}
	return m.AppendBlocks(ctx, key, []Block{{
		Seq:      seq,
		Pos:      pos,
		Revision: 0,
		Type:     BlockTypeText,
		Text:     text,
		Step:     blockStepSentinel,
		MindFSID: turnID,
	}})
}

func (m *Manager) AppendProvisionalToolCall(ctx context.Context, key string, seq int, turnID string, pos int, call agenttypes.ToolCall) error {
	copy := call
	return m.AppendBlocks(ctx, key, []Block{{
		Seq:      seq,
		Pos:      pos,
		Revision: 0,
		Type:     BlockTypeToolCall,
		ToolCall: &copy,
		Step:     blockStepSentinel,
		MindFSID: turnID,
	}})
}

func (m *Manager) ReadProvisionalTurnBlocks(ctx context.Context, key string, seq int, turnID string) ([]Block, error) {
	provisional, _, err := m.readProvisionalTurnState(key, seq, turnID)
	return provisional, err
}

// readProvisionalTurnState 一次流式扫描拿到两样东西：本轮未封口块，以及同一 seq
// 已可见块各 pos 的最大 revision。原先封口要跑两遍全量读（ReadProvisionalTurnBlocks
// 一遍、ReadBlocks 再一遍），长会话 70 MB 时等于白读 140 MB。
func (m *Manager) readProvisionalTurnState(key string, seq int, turnID string) ([]Block, map[int]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	provisional := make([]Block, 0)
	maxRevision := make(map[int]int)
	if err := m.forEachBlock(key, func(block Block) bool {
		if block.Seq != seq {
			return true
		}
		if block.MindFSID == turnID && block.Step < 0 {
			provisional = append(provisional, block)
			return true
		}
		if block.Step >= 0 && block.Revision > maxRevision[block.Pos] {
			maxRevision[block.Pos] = block.Revision
		}
		return true
	}); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(provisional, func(i, j int) bool { return provisional[i].Pos < provisional[j].Pos })
	return provisional, maxRevision, nil
}

// SealProvisionalTurnBlocks 把未封口块追加上高 revision 的可见版本。重复调用安全：
// 每次 revision+1，读取侧同 (seq,pos) 只取最高版本，界面仍只看到一份。
func (m *Manager) SealProvisionalTurnBlocks(ctx context.Context, key string, seq int, turnID string) error {
	provisional, maxRevision, err := m.readProvisionalTurnState(key, seq, turnID)
	if err != nil {
		return err
	}
	if len(provisional) == 0 {
		return nil
	}
	sealed := make([]Block, 0, len(provisional))
	for i := 0; i < len(provisional); i++ {
		block := provisional[i]
		if block.Type == BlockTypeText {
			// 流式正文是一段一段写进来的；封口时把连续的文本片段拼回一条，
			// 否则恢复出来的同一段回复会被界面拆成多个独立气泡。
			var text strings.Builder
			text.WriteString(block.Text)
			for i+1 < len(provisional) && provisional[i+1].Type == BlockTypeText {
				i++
				text.WriteString(provisional[i].Text)
			}
			block.Text = text.String()
		}
		next := block
		next.Revision = maxRevision[block.Pos] + 1
		next.Step = 1
		if next.Type == BlockTypeToolCall && next.ToolCall != nil {
			call := *next.ToolCall
			call.Status = "interrupted"
			call.Title = strings.TrimSpace(call.Title) + "（服务端重启，工具未返回）"
			next.ToolCall = &call
		}
		maxRevision[block.Pos] = next.Revision
		sealed = append(sealed, next)
	}
	return m.AppendBlocks(ctx, key, sealed)
}
