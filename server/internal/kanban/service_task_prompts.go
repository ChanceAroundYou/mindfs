package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
)

// TaskQuestionInput 是会话 ask_user 事件映射到任务对话所需的全部信息。
// Body 是给人看的一行摘要；PayloadJSON 保留结构化 questions/agent，前端据此
// 复用 AskUserQuestionCard 渲染。
type TaskQuestionInput struct {
	TaskID      string
	SessionKey  string
	ToolUseID   string
	Body        string
	PayloadJSON string
}

// RecordTaskQuestion 把一次 ask_user 落成任务对话里的 question 卡。
//
// 幂等键是 (session_key, tool_use_id)：同一个 tool call 被重复广播（SDK 两条
// 路径/重放）只能有一张卡。工作流节点会话会顺带绑定当前活动的 NodeRun。
func (s *ActivityService) RecordTaskQuestion(ctx context.Context, in TaskQuestionInput) (*TaskActivity, bool, error) {
	if s == nil || s.tasks == nil {
		return nil, false, errors.New("activity service task store required")
	}
	in.TaskID = strings.TrimSpace(in.TaskID)
	in.SessionKey = strings.TrimSpace(in.SessionKey)
	in.ToolUseID = strings.TrimSpace(in.ToolUseID)
	if in.TaskID == "" || in.SessionKey == "" || in.ToolUseID == "" {
		return nil, false, errors.New("task id, session key and tool use id required")
	}
	task, err := s.tasks.GetTask(ctx, in.TaskID)
	if err != nil {
		return nil, false, err
	}

	var runID, nodeRunID string
	if nodeRun, err := s.tasks.WorkflowNodeRunBySessionKey(ctx, in.TaskID, in.SessionKey); err == nil {
		runID, nodeRunID = nodeRun.RunID, nodeRun.ID
	} else if !errors.Is(err, ErrWorkflowNodeRunNotFound) {
		return nil, false, err
	}

	body := strings.TrimSpace(in.Body)
	if body == "" {
		body = "需要你确认"
	}
	activity := &TaskActivity{
		ID:          "q-" + taskPromptHash(in.SessionKey, in.ToolUseID),
		TaskID:      in.TaskID,
		RootID:      task.RootID,
		Type:        ActivityTypeQuestion,
		Author:      "agent",
		Role:        RoleAgent,
		Body:        body,
		Status:      ActivityStatusWaitingUser,
		SessionKey:  in.SessionKey,
		ToolUseID:   in.ToolUseID,
		PayloadJSON: strings.TrimSpace(in.PayloadJSON),
		RunID:       runID,
		NodeRunID:   nodeRunID,
	}
	store := NewActivityStore(s.tasks.db)
	created, err := store.InsertOrIgnore(ctx, activity)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, err := store.Get(ctx, activity.ID)
		if err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	s.broadcastCreated(activity)
	return activity, true, nil
}

// taskPromptHash 用 64 位稳定哈希做 Prompt 卡 id。不能截短 session/tool id：
// 同一会话里连续的 tool_use_id 前缀高度相似，截断会撞卡。
func taskPromptHash(sessionKey, toolUseID string) string {
	sum := sha256.Sum256([]byte(sessionKey + "\x00" + toolUseID))
	return hex.EncodeToString(sum[:8])
}

// MarkTaskQuestionAnswered 把任务提问卡收成 completed，并把答案合并进 payload。
//
// 会话查看器和任务对话都走这里；同一张卡重复提交是幂等的（先到的答案赢），
// 不会把后来/重放的答案覆盖上去。
func (s *ActivityService) MarkTaskQuestionAnswered(ctx context.Context, sessionKey, toolUseID string, answers map[string]string) (*TaskActivity, bool, error) {
	if s == nil || s.tasks == nil {
		return nil, false, errors.New("activity service task store required")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	toolUseID = strings.TrimSpace(toolUseID)
	if sessionKey == "" || toolUseID == "" {
		return nil, false, errors.New("session key and tool use id required")
	}
	store := NewActivityStore(s.tasks.db)
	question, err := store.QuestionByToolUseID(ctx, sessionKey, toolUseID)
	if errors.Is(err, ErrActivityNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	payload := map[string]any{}
	if strings.TrimSpace(question.PayloadJSON) != "" {
		if err := json.Unmarshal([]byte(question.PayloadJSON), &payload); err != nil {
			payload = map[string]any{}
		}
	}
	alreadyAnswered := hasPromptAnswers(payload)
	// 空答案只表示会话先收口（tool_call_update complete），不能写 answers:null：
	// 否则迟到的真实答案会被误判成「已经答过」。
	if len(answers) > 0 {
		payload["answers"] = answers
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	if question.Status == ActivityStatusCompleted {
		// 会话先收到 complete、任务卡已 completed 但没答案时，允许真实答案补一次。
		// 以原 payload 做 CAS，重复/并发补写只有第一个生效，不覆盖已有答案。
		if len(answers) == 0 || alreadyAnswered {
			return question, false, nil
		}
		updated, changed, err := store.FillCompletedPromptPayload(ctx, question.ID, question.PayloadJSON, string(encoded))
		if err != nil {
			return nil, false, err
		}
		if changed {
			s.broadcastUpdated(updated, map[string]any{
				"payload_json": string(encoded),
			})
		}
		if changed && len(answers) > 0 {
			s.recordUserAnswerActivity(ctx, store, question, answers)
		}
		return updated, changed, nil
	}
	if question.Status != ActivityStatusWaitingUser {
		return question, false, nil
	}
	updated, changed, err := store.ResolvePrompt(ctx, question.ID, ActivityStatusCompleted, string(encoded), nil)
	if err != nil {
		return nil, false, err
	}
	if changed {
		s.broadcastUpdated(updated, map[string]any{
			"status":       ActivityStatusCompleted,
			"payload_json": string(encoded),
		})
	}
	if changed && len(answers) > 0 {
		s.recordUserAnswerActivity(ctx, store, question, answers)
	}
	return updated, changed, nil
}

// recordUserAnswerActivity 在任务对话里补一条 user_answer 用户行。
//
// 活动 id 由提问卡确定性派生：同一次答题的重放（浏览器重试、两条路径都到）
// 走 InsertOrIgnore，不会长出第二行。答案为空 map 时调用方根本不进来 ——
// 「只收口、没作答」不是用户的答复。
//
// 这是**镜像行**，失败只记日志：答案本身已经写进提问卡 payload 并广播，
// 为了一行展示把整次作答报成失败，用户看到的是「答了但页面报错」。
func (s *ActivityService) recordUserAnswerActivity(
	ctx context.Context,
	store *ActivityStore,
	question *TaskActivity,
	answers map[string]string,
) {
	if question == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"question_id": question.ID,
		"answers":     answers,
	})
	if err != nil {
		log.Printf("[kanban/prompts] answer activity marshal %s: %v", question.ID, err)
		return
	}
	activity := &TaskActivity{
		ID:          "answer-" + taskPromptHash(question.SessionKey, question.ToolUseID),
		TaskID:      question.TaskID,
		RootID:      question.RootID,
		Type:        ActivityTypeUserAnswer,
		Author:      "user",
		Role:        RoleUser,
		Body:        userAnswerBody(answers),
		SessionKey:  question.SessionKey,
		PayloadJSON: string(payload),
	}
	inserted, err := store.InsertOrIgnore(ctx, activity)
	if err != nil {
		log.Printf("[kanban/prompts] answer activity insert %s: %v", activity.ID, err)
		return
	}
	if inserted {
		s.broadcastCreated(activity)
	}
}

// userAnswerBody 把答案渲染成一行人话。答案 key 通常就是问题原文；没有 key 时
// 退回裸答案。排序只为稳定，与提交顺序无关。
func userAnswerBody(answers map[string]string) string {
	keys := make([]string, 0, len(answers))
	for key := range answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(answers[key])
		if value == "" {
			continue
		}
		if label := strings.TrimSpace(key); label != "" {
			parts = append(parts, label+"："+value)
		} else {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "已回答提问"
	}
	return "已回答提问：" + strings.Join(parts, "；")
}

// CloseOpenTaskQuestions 收口某会话里所有仍等待回答的提问卡（cancelled）。
//
// 触发点是回合终结和提问工具失败（failed/cancelled）。任务对话在存在 waiting
// 卡时挡住输入框，所以一张已经不可能回答的卡必须退出等待态，否则用户既答不了
// 也发不了新消息。只动 waiting_user 行；已答的卡（complete 先到/答案已写）
// 不受影响。返回实际收口的卡数。
func (s *ActivityService) CloseOpenTaskQuestions(ctx context.Context, sessionKey, reason string) (int, error) {
	if s == nil || s.tasks == nil {
		return 0, errors.New("activity service task store required")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0, nil
	}
	store := NewActivityStore(s.tasks.db)
	open, err := store.OpenQuestionsBySession(ctx, sessionKey)
	if err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	closed := 0
	for _, question := range open {
		var summary *string
		if reason != "" {
			summary = &reason
		}
		updated, changed, err := store.ResolvePrompt(ctx, question.ID, ActivityStatusCancelled, question.PayloadJSON, summary)
		if err != nil {
			return closed, err
		}
		if !changed {
			continue
		}
		closed++
		changes := map[string]any{"status": ActivityStatusCancelled}
		if summary != nil {
			changes["summary"] = *summary
		}
		s.broadcastUpdated(updated, changes)
	}
	return closed, nil
}

// hasPromptAnswers 判断 payload 里是否已有本次答案。空 map 不算答过，
// 但仍由调用方按「空答案不收口」处理。
func hasPromptAnswers(payload map[string]any) bool {
	answers, ok := payload["answers"].(map[string]any)
	return ok && len(answers) > 0
}
