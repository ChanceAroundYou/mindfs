package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func (s *Service) AnswerQuestion(ctx context.Context, in AnswerQuestionInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	sessionKey := strings.TrimSpace(in.SessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	agentName := strings.TrimSpace(in.Agent)
	if agentName == "" {
		current, err := manager.Get(ctx, sessionKey, 0)
		if err != nil {
			return err
		}
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	if agentName == "" {
		return errors.New("agent required")
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return errors.New("agent pool unavailable")
	}
	sess, ok := pool.Get(agentPoolSessionKey(sessionKey, agentName))
	if !ok {
		return errors.New("agent session not found")
	}
	answer := agenttypes.AskUserAnswer{
		ToolUseID: strings.TrimSpace(in.ToolUseID),
		Answers:   in.Answers,
	}
	answeredAt := time.Now()
	if err := manager.MarkPendingAskUserAnswered(ctx, sessionKey, answer.ToolUseID, answer.Answers, answeredAt); err != nil {
		return err
	}
	return deliverAskUserAnswer(ctx, manager, sess, sessionKey, answer, answeredAt)
}

// deliverAskUserAnswer 把答案投给 agent，投递失败时撤回刚写下的「已回答」标记。
//
// 两步的先后是刻意的，两边都不能挪：
//   - 标记先写：投递成功会让 agent 立刻继续，pending 条目随后可能被收尾清掉，那时再写就
//     写不进去了（MarkPendingAskUserAnswered 找不到条目会报 pending tool call not found），
//     结果是答案送出去了、库里却没有，刷新后卡片又变回「可回答」。
//   - 但标记写下之后投递失败，这个标记就成了谎：卡片显示「已回答」而 agent 仍在等这次
//     权限应答 —— 该轮永久卡死，用户也没有第二次机会（前端提交后本地就把卡片翻成已回答，
//     且不消费服务端的失败回执）。所以失败必须撤回，让卡片回到可回答状态。
func deliverAskUserAnswer(ctx context.Context, manager *session.Manager, sess agenttypes.Session, sessionKey string, answer agenttypes.AskUserAnswer, answeredAt time.Time) error {
	if err := sess.AnswerQuestion(ctx, answer); err != nil {
		manager.UnmarkPendingAskUserAnswered(ctx, sessionKey, answer.ToolUseID, answeredAt)
		return err
	}
	return nil
}

func currentAssistantLine(responseText string) int {
	// Anchor auxiliary events after the last text line, before trailing empty
	// lines that may be filled by subsequent chunks. Leave the response intact.
	responseText = strings.TrimRight(responseText, "\n")
	if responseText == "" {
		return 0
	}
	return strings.Count(responseText, "\n") + 1
}

func hydratePendingToolCallAux(ctx context.Context, manager *session.Manager, sessionKey string, aux session.ExchangeAux) session.ExchangeAux {
	if manager == nil || aux.ToolCall == nil || strings.TrimSpace(aux.ToolCall.CallID) == "" {
		return aux
	}
	if aux.ToolCall.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	latest, err := manager.GetFullToolCall(ctx, sessionKey, aux.ToolCall.CallID)
	if err != nil || latest == nil {
		return aux
	}
	if latest.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	toolCall := *latest
	aux.ToolCall = &toolCall
	return aux
}

func dedupeExchangeAuxBuffer(items []session.ExchangeAux) []session.ExchangeAux {
	if len(items) == 0 {
		return nil
	}
	seenToolCallIDs := make(map[string]int, len(items))
	seenPlanIDs := make(map[string]struct{}, len(items))
	seenCompactIDs := make(map[string]struct{}, len(items))
	seenTodo := false
	out := make([]session.ExchangeAux, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		callID := ""
		if item.ToolCall != nil {
			callID = strings.TrimSpace(item.ToolCall.CallID)
		}
		if callID != "" {
			if existingIndex, exists := seenToolCallIDs[callID]; exists {
				if out[existingIndex].ToolCall != nil {
					merged := mergeBufferedToolCall(*item.ToolCall, *out[existingIndex].ToolCall)
					out[existingIndex].ToolCall = &merged
				}
				continue
			}
			seenToolCallIDs[callID] = len(out)
		}
		isTodo := item.Todo != nil || (item.ToolCall != nil && item.ToolCall.Kind == agenttypes.ToolKindTodo)
		if isTodo {
			if seenTodo {
				continue
			}
			seenTodo = true
		}
		if item.Plan != nil {
			planID := strings.TrimSpace(item.Plan.ID)
			if planID != "" {
				if _, exists := seenPlanIDs[planID]; exists {
					continue
				}
				seenPlanIDs[planID] = struct{}{}
			}
		}
		if item.Compact != nil {
			compactID := strings.TrimSpace(item.Compact.ID)
			if compactID != "" {
				if _, exists := seenCompactIDs[compactID]; exists {
					continue
				}
				seenCompactIDs[compactID] = struct{}{}
			}
		}
		out = append(out, item)
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func shouldPersistToolCallAux(toolCall agenttypes.ToolCall) bool {
	if toolCall.RawType != "claude_task" {
		return true
	}
	return strings.TrimSpace(stringMeta(toolCall.Meta, "subtype")) != "task_progress"
}

func mergeBufferedToolCall(base, next agenttypes.ToolCall) agenttypes.ToolCall {
	merged := base
	if strings.TrimSpace(next.CallID) != "" {
		merged.CallID = next.CallID
	}
	if strings.TrimSpace(next.Title) != "" {
		merged.Title = next.Title
	}
	if strings.TrimSpace(next.Status) != "" {
		merged.Status = next.Status
	}
	if next.Kind != "" {
		merged.Kind = next.Kind
	}
	if len(next.Content) > 0 {
		merged.Content = append([]agenttypes.ToolCallContentItem(nil), next.Content...)
	}
	if len(next.Locations) > 0 {
		merged.Locations = append([]agenttypes.ToolCallLocation(nil), next.Locations...)
	}
	if strings.TrimSpace(next.RawType) != "" {
		merged.RawType = next.RawType
	}
	if len(base.Meta) > 0 || len(next.Meta) > 0 {
		merged.Meta = make(map[string]any, len(base.Meta)+len(next.Meta))
		for key, value := range base.Meta {
			merged.Meta[key] = value
		}
		for key, value := range next.Meta {
			merged.Meta[key] = value
		}
		if stringMeta(base.Meta, "taskTool") == "TaskCreate" && stringMeta(next.Meta, "taskTool") == "TaskUpdate" {
			merged.Meta["taskTool"] = "TaskCreate"
		}
	}
	return merged
}

func appendResponseChunk(responseText, lastResponseUpdateType, chunk string) string {
	if responseText != "" &&
		(lastResponseUpdateType == string(agenttypes.EventTypeThoughtChunk) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolCall) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeTodoUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypePlanUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeCompact)) &&
		!strings.HasSuffix(responseText, "\n\n") &&
		!strings.HasSuffix(responseText, "\n") {
		responseText += "\n\n"
	}
	return responseText + chunk
}
