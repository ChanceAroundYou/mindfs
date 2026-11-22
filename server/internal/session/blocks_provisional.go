package session

import (
	"context"
	"sort"

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
	all, err := m.ReadBlocks(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0)
	for _, block := range all {
		if block.Seq == seq && block.MindFSID == turnID && block.Step < 0 {
			out = append(out, block)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out, nil
}

// SealProvisionalTurnBlocks 把未封口块追加上高 revision 的可见版本。重复调用安全：
// 每次 revision+1，读取侧同 (seq,pos) 只取最高版本，界面仍只看到一份。
func (m *Manager) SealProvisionalTurnBlocks(ctx context.Context, key string, seq int, turnID string) error {
	provisional, err := m.ReadProvisionalTurnBlocks(ctx, key, seq, turnID)
	if err != nil {
		return err
	}
	if len(provisional) == 0 {
		return nil
	}
	all, err := m.ReadBlocks(ctx, key)
	if err != nil {
		return err
	}
	maxRevision := make(map[int]int)
	for _, block := range all {
		if block.Seq == seq && block.Step >= 0 && block.Revision > maxRevision[block.Pos] {
			maxRevision[block.Pos] = block.Revision
		}
	}
	sealed := make([]Block, 0, len(provisional))
	for _, block := range provisional {
		next := block
		next.Revision = maxRevision[block.Pos] + 1
		next.Step = 1
		if next.Type == BlockTypeToolCall && next.ToolCall != nil {
			call := *next.ToolCall
			call.Status = "interrupted"
			next.ToolCall = &call
		}
		maxRevision[block.Pos] = next.Revision
		sealed = append(sealed, next)
	}
	return m.AppendBlocks(ctx, key, sealed)
}
