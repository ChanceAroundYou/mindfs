package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// claimAskUserEmit 为一次 AskUserQuestion 的发证：首次调用返回 true（调用方负责发射），
// 重复调用返回 false。SDK 的两条路径（CanUseTool / AssistantMessage）都可能触发，
// 用集合让「先到者发射、后到者跳过」，不依赖两条路径的先后次序。
func (s *session) claimAskUserEmit(callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return true
	}
	s.questionMu.Lock()
	defer s.questionMu.Unlock()
	if s.emittedAskUsers == nil {
		s.emittedAskUsers = make(map[string]struct{})
	}
	if _, exists := s.emittedAskUsers[callID]; exists {
		return false
	}
	s.emittedAskUsers[callID] = struct{}{}
	return true
}

type askUserAnswerResult struct {
	answers claudeagent.Answers
	err     error
}

func (s *session) AnswerQuestion(ctx context.Context, answer types.AskUserAnswer) error {
	callID := strings.TrimSpace(answer.ToolUseID)
	if callID == "" {
		return errors.New("toolUseId required")
	}
	if len(answer.Answers) == 0 {
		return errors.New("answers required")
	}

	waiter, err := s.askUserWaiter(ctx, callID)
	if err != nil {
		return err
	}

	answers := make(claudeagent.Answers, len(answer.Answers))
	for key, value := range answer.Answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			answers[key] = value
		}
	}
	if len(answers) == 0 {
		return errors.New("answers required")
	}

	select {
	case waiter <- askUserAnswerResult{answers: answers}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// askUserWaiterGrace 是「卡片已经能看见、应答通道却还没登记」这个窗口的容忍上限。
// 变量而非常量只为让测试把窗口压到毫秒级。
var askUserWaiterGrace = 2 * time.Second

// askUserWaiter 取该提问的应答通道。
//
// 同一次 AskUserQuestion 由**两条独立的 SDK 路径**送达：卡片由 finalized
// AssistantMessage 的 tool_use 块发出（先到），而这里要取的应答通道由 can_use_tool
// 权限回调登记（后到，真实 CLI 实测晚约 74ms）。于是存在一个窗口：卡片已经渲染出来、
// 用户（或任何快速客户端）此时提交的答案却会被「question is not pending」拒掉。
//
// 拒掉的后果不是"稍后重试"而是**永久卡死**：前端提交答案后本地就把卡片翻成「已回答」，
// 而 CLI 仍在等这一次权限应答，该轮再也不会前进，用户也没有第二次机会。
//
// 所以这里等真值出现再判死：短轮询到上限仍未登记，才认为这个 callID 确实不是待答提问。
func (s *session) askUserWaiter(ctx context.Context, callID string) (chan askUserAnswerResult, error) {
	deadline := time.Now().Add(askUserWaiterGrace)
	for {
		s.questionMu.Lock()
		waiter, ok := s.questionWaits[callID]
		s.questionMu.Unlock()
		if ok {
			return waiter, nil
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("question is not pending: " + callID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *session) handleCanUseTool(ctx context.Context, req claudeagent.ToolPermissionRequest) claudeagent.PermissionResult {
	if req.ToolName != "AskUserQuestion" {
		return claudeagent.PermissionAllow{}
	}

	// 参数读不出来 / 一个问题都没有：**不能放行**。放行等于让 CLI 自行执行这次提问，
	// 而它拿不到任何答案 —— 模型看到的是「用户没有回答」这个凭空捏造的结果（且无日志）。
	// 拒绝会把真实原因作为 tool_result 回给模型，它可以据此换个问法或直接继续。
	var input claudeagent.AskUserQuestionInput
	if err := json.Unmarshal(req.Arguments, &input); err != nil {
		return claudeagent.PermissionDeny{Reason: "ask user question arguments are not readable: " + err.Error()}
	}
	if len(input.Questions) == 0 {
		return claudeagent.PermissionDeny{Reason: "ask user question has no questions"}
	}

	callID := strings.TrimSpace(req.Context.ToolUseID)
	if callID == "" {
		return claudeagent.PermissionDeny{Reason: "ask user question missing tool use id"}
	}

	answers, err := s.awaitAskUserQuestion(ctx, claudeagent.QuestionSet{
		ToolUseID: callID,
		Questions: input.Questions,
		SessionID: req.Context.SessionID,
	})
	if err != nil {
		return claudeagent.PermissionDeny{Reason: err.Error()}
	}

	updatedInput := make(map[string]interface{})
	if err := json.Unmarshal(req.Arguments, &updatedInput); err != nil {
		updatedInput["questions"] = input.Questions
	}
	updatedInput["answers"] = normalizeAskUserAnswers(input.Questions, answers)

	return claudeagent.PermissionAllow{UpdatedInput: updatedInput}
}

func normalizeAskUserAnswers(questions []claudeagent.QuestionItem, answers claudeagent.Answers) map[string]string {
	normalized := make(map[string]string, len(answers))

	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || askUserQuestionTextForKey(key, questions) != "" {
			continue
		}
		normalized[key] = value
	}
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		questionText := askUserQuestionTextForKey(key, questions)
		if questionText == "" {
			questionText = key
		}
		if _, exists := normalized[questionText]; !exists {
			normalized[questionText] = value
		}
	}

	return normalized
}

func askUserQuestionTextForKey(key string, questions []claudeagent.QuestionItem) string {
	for index, question := range questions {
		if key == fmt.Sprintf("q_%d", index) {
			return strings.TrimSpace(question.Question)
		}
	}
	return ""
}

func (s *session) cancelPendingQuestions(err error) {
	if err == nil {
		err = errors.New("turn canceled")
	}
	s.questionMu.Lock()
	type pendingQuestion struct {
		callID string
		waiter chan askUserAnswerResult
	}
	waiters := make([]pendingQuestion, 0, len(s.questionWaits))
	for callID, waiter := range s.questionWaits {
		waiters = append(waiters, pendingQuestion{callID: callID, waiter: waiter})
		delete(s.questionWaits, callID)
	}
	s.questionMu.Unlock()

	for _, pending := range waiters {
		select {
		case pending.waiter <- askUserAnswerResult{err: err}:
		default:
		}
		if update, ok := s.cancelPendingToolCall(pending.callID, err.Error()); ok {
			s.emit(types.Event{
				Type:      types.EventTypeToolUpdate,
				SessionID: s.SessionID(),
				Data:      update,
			})
		}
	}
}
