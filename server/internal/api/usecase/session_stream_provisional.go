package usecase

import (
	"context"
	"log"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// provisionalStreamWriter 把流式正文/工具卡追加成未封口块。写失败只记日志：
// 它守的是「崩溃后能补回」，绝不能因为磁盘问题打断正在进行的模型回合。
type provisionalStreamWriter struct {
	manager *session.Manager
	key     string
	seq     int
	turnID  string
	pos     int
	tools   map[string]bool
}

func newProvisionalStreamWriter(manager *session.Manager, key string, userSeq int, turnID string) *provisionalStreamWriter {
	if manager == nil || userSeq <= 0 || turnID == "" {
		return nil
	}
	return &provisionalStreamWriter{manager: manager, key: key, seq: userSeq + 1, turnID: turnID, tools: map[string]bool{}}
}

func (w *provisionalStreamWriter) nextPos() int {
	pos := w.pos
	w.pos++
	return pos
}

func (w *provisionalStreamWriter) text(content string) {
	if w == nil || content == "" {
		return
	}
	if err := w.manager.AppendProvisionalText(context.Background(), w.key, w.seq, w.turnID, w.nextPos(), content); err != nil {
		log.Printf("[session/provisional] text.error session=%s turn=%s err=%v", w.key, w.turnID, err)
	}
}

func (w *provisionalStreamWriter) toolCall(call agenttypes.ToolCall) {
	if w == nil || call.CallID == "" || w.tools[call.CallID] {
		return
	}
	w.tools[call.CallID] = true
	if err := w.manager.AppendProvisionalToolCall(context.Background(), w.key, w.seq, w.turnID, w.nextPos(), call); err != nil {
		log.Printf("[session/provisional] tool_call.error session=%s turn=%s call=%s err=%v", w.key, w.turnID, call.CallID, err)
	}
}
