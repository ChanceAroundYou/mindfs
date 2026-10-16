package acp

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	acpsdk "github.com/coder/acp-go-sdk"
)

// initializeTimeout 给 ACP 握手封顶。
//
// 握手跑在 pool 生命周期的 processCtx 上，本身没有 deadline；而调用方持有
// per-agent 运行时锁。一个不回应 initialize 的 agent 会让它永不返回，该 agent
// 之后每个请求都堵在同一把锁上，永远停在 available=false / "probe pending"
// （2026-10-02 dsh 卡死 58 分钟就是这个形态）。握手只是启动后一次 JSON-RPC
// 往返，60s 已远超任何正常冷启动；池里的探测预算（probeSessionTimeout=45s）
// 比它还短，所以先放弃的仍然是探测本身。
var initializeTimeout = 60 * time.Second

type session struct {
	proc          *Process
	sessionKey    string
	agentDebugLog *logs.AgentLogger
}

func (s *session) SendMessage(ctx context.Context, content string) error {
	return s.proc.SendMessage(ctx, s.sessionKey, content)
}

// AnswerQuestion 把前端提交的答案投递给挂起的提问。
//
// CUSTOM(G-AS): 上游这里是硬编码的 "ask user question is not supported by acp
// sessions"。dsh 的提问走 elicitation，答案由 Process 编码后回给 agent。
func (s *session) AnswerQuestion(ctx context.Context, answer types.AskUserAnswer) error {
	return s.proc.AnswerElicitation(ctx, answer.ToolUseID, answer.Answers)
}

func (s *session) CurrentModel() string {
	if s == nil || s.proc == nil {
		return ""
	}
	if current := strings.TrimSpace(mapModelConfigOptions(s.proc.SessionConfigOptions(s.sessionKey)).CurrentModelID); current != "" {
		return current
	}
	return strings.TrimSpace(mapModelState(s.proc.SessionModelState(s.sessionKey)).CurrentModelID)
}

func (s *session) SetModel(ctx context.Context, model string) error {
	if s == nil || s.proc == nil {
		return errors.New("acp session not initialized")
	}
	return s.proc.SetModel(ctx, s.sessionKey, model)
}

func (s *session) ListModels(_ context.Context) (types.ModelList, error) {
	if s == nil || s.proc == nil {
		return types.ModelList{}, errors.New("acp session not initialized")
	}
	if models := mapModelConfigOptions(s.proc.SessionConfigOptions(s.sessionKey)); len(models.Models) > 0 || models.CurrentModelID != "" {
		return models, nil
	}
	return mapModelState(s.proc.SessionModelState(s.sessionKey)), nil
}

func (s *session) SetMode(ctx context.Context, mode string) error {
	if s == nil || s.proc == nil {
		return errors.New("acp session not initialized")
	}
	return s.proc.SetMode(ctx, s.sessionKey, mode)
}

func (s *session) SetPlanMode(_ context.Context, _ bool) error {
	return nil
}

func (s *session) ListModes(_ context.Context) (types.ModeList, error) {
	if s == nil || s.proc == nil {
		return types.ModeList{}, errors.New("acp session not initialized")
	}
	if modes := mapModeConfigOptions(s.proc.SessionConfigOptions(s.sessionKey)); len(modes.Modes) > 0 || modes.CurrentModeID != "" {
		return modes, nil
	}
	return mapModeState(s.proc.SessionModeState(s.sessionKey)), nil
}

func (s *session) ListCommands(_ context.Context) (types.CommandList, error) {
	if s == nil || s.proc == nil {
		return types.CommandList{}, errors.New("acp session not initialized")
	}
	return mapCommandState(s.proc.SessionCommands(s.sessionKey)), nil
}

func (s *session) CancelCurrentTurn() error {
	return s.proc.CancelCurrentTurn(s.sessionKey)
}

func (s *session) OnUpdate(onUpdate func(types.Event)) {
	s.proc.SetOnUpdate(s.sessionKey, func(update SessionUpdate) {
		s.logRawToolUpdate(update)
		if update.Type == UpdateTypeUserMessage {
			return
		}
		if onUpdate != nil {
			if update.Type == UpdateTypeMessageDone {
				contextWindow, _ := s.ContextWindow(context.Background())
				onUpdate(types.Event{
					Type:      types.EventTypeMessageDone,
					SessionID: update.SessionID,
					Data:      types.MessageDone{ContextWindow: contextWindow, TokenUsage: update.TokenUsage},
				})
				return
			}
			ev := convertEvent(update, func(callID string, questions []elicitationQuestion) {
				s.proc.registerPendingAskUser(callID, s.sessionKey, questions)
			})
			if ev.Type == "" {
				return
			}
			// CUSTOM(G-AS): 工具卡进终态时把没被 elicitation 绑定的条目清掉，免得泄漏。
			if toolCall, ok := ev.Data.(types.ToolCall); ok && toolCall.Kind == types.ToolKindAskUser {
				s.proc.reapPendingAskUser(toolCall.CallID, toolCall.Status)
			}
			onUpdate(ev)
		}
	})
}

func (s *session) SessionID() string {
	return s.proc.SessionID(s.sessionKey)
}

func (s *session) ContextWindow(_ context.Context) (types.ContextWindow, error) {
	if s == nil || s.proc == nil {
		return types.ContextWindow{}, errors.New("acp session not initialized")
	}
	return s.proc.SessionContextWindow(s.sessionKey), nil
}

func (s *session) RuntimeDefaults(context.Context) (types.RuntimeDefaults, error) {
	if s == nil || s.proc == nil {
		return types.RuntimeDefaults{}, errors.New("acp session not initialized")
	}
	options := s.proc.SessionConfigOptions(s.sessionKey)
	model := configOptionCurrentValue(options, acpsdk.SessionConfigOptionCategoryModel)
	if model == "" {
		model = mapModelState(s.proc.SessionModelState(s.sessionKey)).CurrentModelID
	}
	return types.RuntimeDefaults{
		Model:  model,
		Effort: configOptionCurrentValue(options, acpsdk.SessionConfigOptionCategoryThoughtLevel),
	}, nil
}

func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.proc.CloseSession(ctx, s.sessionKey)
}

func convertACPPlanUpdate(update acpsdk.SessionPlanUpdate) (types.EventType, any) {
	content := update.Plan
	switch {
	case content.Markdown != nil:
		return types.EventTypePlanUpdate, types.PlanUpdate{
			ID:      string(content.Markdown.Id),
			Content: strings.TrimSpace(content.Markdown.Content),
		}
	case content.Items != nil:
		return types.EventTypeTodoUpdate, types.TodoUpdate{Items: convertACPPlanEntriesToTodos(content.Items.Entries)}
	case content.File != nil:
		return types.EventTypePlanUpdate, types.PlanUpdate{
			ID:      string(content.File.Id),
			Content: strings.TrimSpace("Plan file: " + content.File.Uri),
		}
	default:
		return types.EventTypePlanUpdate, types.PlanUpdate{}
	}
}

func acpPlanUpdateID(update acpsdk.SessionPlanUpdate) string {
	content := update.Plan
	switch {
	case content.Markdown != nil:
		return string(content.Markdown.Id)
	case content.Items != nil:
		return string(content.Items.Id)
	case content.File != nil:
		return string(content.File.Id)
	default:
		return ""
	}
}

func convertACPPlanEntriesToTodos(entries []acpsdk.PlanEntry) []types.TodoItem {
	if len(entries) == 0 {
		return nil
	}
	items := make([]types.TodoItem, 0, len(entries))
	for _, entry := range entries {
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		items = append(items, types.TodoItem{
			Content: content,
			Status:  normalizeACPPlanStatus(entry.Status),
		})
	}
	return items
}

func normalizeACPPlanStatus(status acpsdk.PlanEntryStatus) string {
	switch status {
	case acpsdk.PlanEntryStatusCompleted:
		return "completed"
	case acpsdk.PlanEntryStatusInProgress:
		return "in_progress"
	default:
		return "pending"
	}
}

func logUnhandledConvertEvent(update SessionUpdate, scope string) {
	log.Printf(
		"[agent/acp] unhandled.%s raw=%s",
		scope,
		truncateRawJSON(update.Raw),
	)
}

func truncateRawJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return `{"marshal_error":true}`
	}
	const maxRawLogBytes = 1024
	if len(raw) > maxRawLogBytes {
		raw = append(raw[:maxRawLogBytes], []byte("...(truncated)")...)
	}
	return string(raw)
}

func convertToolCallContent(items []acpsdk.ToolCallContent) []types.ToolCallContentItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.ToolCallContentItem, 0, len(items))
	for _, item := range items {
		if item.Content != nil {
			contentItem := types.ToolCallContentItem{Type: "text"}
			block := item.Content.Content
			if block.Text != nil {
				contentItem.Text = block.Text.Text
				out = append(out, contentItem)
			}
			continue
		}
		if item.Diff != nil {
			out = append(out, types.ToolCallContentItem{
				Type:    "diff",
				Path:    item.Diff.Path,
				OldText: item.Diff.OldText,
				NewText: item.Diff.NewText,
			})
			continue
		}
	}
	return out
}
