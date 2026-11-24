package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Router 提问卡：把 Router 的 ask_user 落成一张**真的**提问卡（type=question）。
//
// 为什么必须升级：等待态的 agent 回复在前端只是「一条没说完的话」——不锁输入框、
// 没有按钮、答案也不会以任何形式回到 Router。线上 #68 的四条待拍板事项就是这么
// 漂走的：Router 把问题写进正文，用户读完只能自己再打一遍，而 Router 收不到
// 「哪一项被拍成了什么」。
//
// type=question 一次给齐三样东西：卡片按钮（payload.questions）、输入框锁
// （pendingTaskPrompt 只认 question / human_gate）、以及一个明确的作答入口
// （AnswerQuestionCard 把答案写成任务留言）。正文仍然保留问题与选项，飞书通知
// 与纯文本客户端读的是它。

// routerQuestionSource 标记「这张卡是 Router 发起的」，而不是某个会话里的
// tool call。作答路径靠它分流：会话里的卡必须把答案送回会话，Router 的卡必须
// 变成一条用户留言。
const routerQuestionSource = "router"

// routerQuestionHeader 是 Router 提问卡的标题前缀。Router 的 ask_user 只有
// 一个 question 字段，没有会话卡那种 header；给一个固定标题，用户一眼能看出
// 这是「需要拍板」而不是「Agent 在报告」。
const routerQuestionHeader = "需要你拍板"

// routerQuestionSupersededSummary 写进被取代的旧卡，作为收口原因。
const routerQuestionSupersededSummary = "已被更新的提问取代"

var (
	// ErrQuestionSessionBound 表示这张提问卡属于某个会话里的 tool call：答案
	// 必须回到那个会话（tool_result），当成任务留言派发会让 Agent 收不到。
	ErrQuestionSessionBound = errors.New("question is bound to a session")
	// ErrQuestionAnswerRequired 表示提交里没有任何答案。
	ErrQuestionAnswerRequired = errors.New("question answer required")
)

// routerQuestionItem 是提问卡载荷里的一道题。字段与会话卡的
// AskUserQuestionItem 同形 —— 两处共用同一个渲染组件。
type routerQuestionItem struct {
	Question string `json:"question"`
	Header   string `json:"header"`
}

// routerQuestionPayload 是 Router 提问卡的载荷：与会话卡同形，额外带一个
// source 用于分流作答路径（见 routerQuestionSource）。
type routerQuestionPayload struct {
	Source    string               `json:"source"`
	Questions []routerQuestionItem `json:"questions"`
	Answers   map[string]string    `json:"answers,omitempty"`
}

// routerAskUserQuestionPayload 把 Router 的提问渲染成提问卡载荷。
//
// 载荷结构与会话里的 AskUserQuestionCard 完全一致（questions[].question /
// header / options[].label）：两处共用同一个渲染组件，形状分叉就会一边能点、
// 一边点不动。
func routerAskUserQuestionPayload(question string, options []string, decisionID string) (string, error) {
	item := map[string]any{
		"question": strings.TrimSpace(question),
		"header":   routerQuestionHeader,
	}
	if labels := routeOptionLabels(options); len(labels) > 0 {
		opts := make([]map[string]any, 0, len(labels))
		for _, label := range labels {
			opts = append(opts, map[string]any{"label": label})
		}
		item["options"] = opts
	}
	payload := map[string]any{
		"source":    routerQuestionSource,
		"questions": []any{item},
	}
	if id := strings.TrimSpace(decisionID); id != "" {
		payload["router_decision_id"] = id
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// closeRouterQuestionsBefore 收口同一任务里更早的 Router 提问卡。
//
// 前端锁输入框读的是**最早**那张等待卡（pendingTaskPrompt）：新卡落地时旧卡不
// 收口，用户点到的就是已经过时的问题，Router 等的却是新问题 —— #104 里问答对
// 不上的形态。只动 Router 自己的卡（session_key 为空）：会话里的提问卡属于某个
// tool call，归会话事件管，关掉会让那个 Agent 永远等不到答案。
func (s *ActivityService) closeRouterQuestionsBefore(ctx context.Context, taskID, keepID string) error {
	if s == nil || s.activities == nil {
		return nil
	}
	open, err := s.activities.OpenRouterQuestionsByTask(ctx, taskID)
	if err != nil {
		return err
	}
	reason := routerQuestionSupersededSummary
	for _, old := range open {
		if old.ID == keepID {
			continue
		}
		updated, changed, err := s.activities.ResolvePrompt(
			ctx, old.ID, ActivityStatusCancelled, old.PayloadJSON, &reason)
		if err != nil {
			return err
		}
		// changed=false 说明这一行在读到之后已被别的路径收口（并发作答/重放）：
		// 别人的结论优先，不覆盖。
		if changed && updated != nil {
			s.broadcastUpdated(updated, map[string]any{
				"status":  ActivityStatusCancelled,
				"summary": reason,
			})
		}
	}
	return nil
}

// AnswerQuestionCard 回答一张 Router 发起的提问卡（网页点击）。
//
// 与飞书那一路（CreateUserReply）是同一件事：答案要成为**下一条用户留言**，
// Router 才能拿到一个 user_message 回合。这里多做两件网页独有的事：
//   - 多题时把「问题 → 答案」逐条写进留言正文，Router 读到的是一条说得清的
//     普通留言，不需要知道提问卡存在；
//   - 答案回写卡片 payload.answers 并把卡收口：按钮变灰、输入框解锁。
//
// 顺序是先留言、后收口：CreateUserReply 要在卡还停在 waiting_user 时认领这次
// 作答，先收口会让它把答案当成「已经答过」直接丢弃。收口失败时卡仍是打开状态，
// 用户重试会命中已有留言这条幂等分支，再收口一次 —— 不会写出第二条留言。
func (s *ActivityService) AnswerQuestionCard(
	ctx context.Context,
	taskID, questionID string,
	answers map[string]string,
) (*TaskActivity, *TaskActivity, error) {
	if s == nil || s.activities == nil {
		return nil, nil, errors.New("activity service not configured")
	}
	taskID = strings.TrimSpace(taskID)
	questionID = strings.TrimSpace(questionID)
	if taskID == "" || questionID == "" {
		return nil, nil, ErrActivityNotFound
	}
	question, err := s.activities.Get(ctx, questionID)
	if err != nil {
		return nil, nil, err
	}
	if question.TaskID != taskID || question.Type != ActivityTypeQuestion {
		return nil, nil, ErrActivityNotFound
	}
	var payload routerQuestionPayload
	if raw := strings.TrimSpace(question.PayloadJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, nil, fmt.Errorf("question payload: %w", err)
		}
	}
	if payload.Source != routerQuestionSource {
		return nil, nil, ErrQuestionSessionBound
	}
	text := questionAnswerBody(payload.Questions, answers)
	if text == "" {
		return nil, nil, ErrQuestionAnswerRequired
	}
	userAct, agentAct, err := s.CreateUserReply(ctx, taskID, text, questionID)
	if err != nil {
		return nil, nil, err
	}
	// 卡片收口：答案与状态一次写进同一行，前端不会看到「有答案但仍等待」的中间态。
	var encodedPayload string
	if merged, err := mergeQuestionAnswers(question.PayloadJSON, answers); err != nil {
		return userAct, agentAct, err
	} else {
		encodedPayload = merged
	}
	updated, changed, err := s.activities.ResolvePrompt(
		ctx, questionID, ActivityStatusCompleted, encodedPayload, nil)
	if err != nil {
		return userAct, agentAct, err
	}
	if changed && updated != nil {
		s.broadcastUpdated(updated, map[string]any{
			"status":       ActivityStatusCompleted,
			"payload_json": encodedPayload,
		})
	}
	return userAct, agentAct, nil
}

// questionAnswerBody 把结构化的问答写成一条用户留言正文。
//
// 形如「是否上线 → 上线」：问题在前、答案在后，一行一条。Router 读到的是普通
// 留言，但「哪一项被拍成了什么」一目了然，它不必再去猜 q_0 是谁。
func questionAnswerBody(questions []routerQuestionItem, answers map[string]string) string {
	lines := make([]string, 0, len(answers))
	for index, item := range questions {
		answer := strings.TrimSpace(answers[fmt.Sprintf("q_%d", index)])
		if answer == "" {
			continue
		}
		label := strings.TrimSpace(item.Question)
		if label == "" {
			label = strings.TrimSpace(item.Header)
		}
		if label == "" {
			lines = append(lines, answer)
			continue
		}
		lines = append(lines, label+" → "+answer)
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	// 结构化题目对不上（手写请求、旧格式）：把非空答案按 key 排序原样拼上。
	// 排序是为了让同一次提交的正文稳定 —— Router 的幂等与复读都依赖这一点。
	keys := make([]string, 0, len(answers))
	for key := range answers {
		if strings.TrimSpace(answers[key]) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, strings.TrimSpace(answers[key]))
	}
	return strings.Join(lines, "\n")
}

// mergeQuestionAnswers 把答案并进卡片原有的 payload。answers 为空时不写，
// 保留原样（与 RecordTaskQuestion 同一口径：空答案不等于答过）。
func mergeQuestionAnswers(payloadJSON string, answers map[string]string) (string, error) {
	payload := map[string]any{}
	if raw := strings.TrimSpace(payloadJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return "", fmt.Errorf("question payload: %w", err)
		}
	}
	if len(answers) > 0 {
		payload["answers"] = answers
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
