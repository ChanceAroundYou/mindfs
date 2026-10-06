package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	types "mindfs/server/internal/agent/types"
)

// dshUserQuestionsMetaKey 是 openma 适配器在 elicitation/create 的 _meta 里携带题目的键名。
const dshUserQuestionsMetaKey = "dsh.userQuestions"

// elicitationBindWait 是等待 tool_call 通知落地的窗口。
//
// ACP SDK 对 request 立即开 goroutine 处理，session/update 通知却走队列由另一个
// goroutine 顺序处理（connection.go 的 receive/processNotifications），所以
// elicitation/create 可能比对应的 tool_call 通知先到。题目 id 与文本在两边
// 逐字相同，用这个窗口轮询就能稳定关联上。
//
// 是 var 而不是 const：测试要把它调短，否则每条「等不到就放弃」的用例都要真等 2s。
var (
	elicitationBindWait = 2 * time.Second
	// elicitationBindPoll 是轮询间隔。
	elicitationBindPoll = 20 * time.Millisecond
)

// pendingAskUser 是「已渲染成 ask_user 卡、正在等用户回答」的提问。
//
// CUSTOM(G-AS): ACP 侧没有上游对应实现。dsh 的 ask_user_question 走 ACP elicitation，
// mindfs 必须自己把工具卡、提问请求与答案回传接起来。
type pendingAskUser struct {
	callID     string // ACP toolCallId，也是前端 tool_use_id
	sessionKey string
	questions  []elicitationQuestion
	waiter     chan elicitationResult
	bound      bool
}

// elicitationQuestion 是归一化后的题目。Options 只存 Label，下标即 option_<j> 的 j。
type elicitationQuestion struct {
	ID          string
	Question    string
	Header      string
	Options     []string
	MultiSelect bool
}

func (q elicitationQuestion) hasOptions() bool { return len(q.Options) > 0 }

// elicitationResult 是答案或取消。content 为空表示 decline / cancel。
type elicitationResult struct {
	content map[string]any
}

// dshUserQuestionsPayload 是 _meta["dsh.userQuestions"] 的形状。
//
// 注意 multiSelect 是**驼峰**：_meta 里的题目来自 user-questions 这条 seam 的对象
// （`@deepseek-ai/dsh-tool-ask-user` 把工具参数 `multi_select` 转成 `multiSelect` 后再
// 交给 seam，openma 适配器原样塞进 _meta），而工具卡 rawInput 里是工具参数的
// 蛇形 `multi_select`。两个来源的大小写不同，别照抄。
type dshUserQuestionsPayload struct {
	Version   int                      `json:"version"`
	Questions []dshUserQuestionPayload `json:"questions"`
}

type dshUserQuestionPayload struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Header   string `json:"header"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
	MultiSelect bool `json:"multiSelect"`
}

// registerPendingAskUser 把一张 ask_user 卡登记为「在等回答」。
//
// first-wins：同一 callID 的后续 tool_call_update 不覆盖已登记的条目，否则已经绑定到
// 该条目的 elicitation 会丢 waiter。
func (p *Process) registerPendingAskUser(callID, sessionKey string, questions []elicitationQuestion) {
	callID = strings.TrimSpace(callID)
	if callID == "" || len(questions) == 0 {
		return
	}
	p.elicitationMu.Lock()
	defer p.elicitationMu.Unlock()
	if _, exists := p.pendingAskUserByCallID[callID]; exists {
		return
	}
	p.pendingAskUserByCallID[callID] = &pendingAskUser{
		callID:     callID,
		sessionKey: sessionKey,
		questions:  questions,
		waiter:     make(chan elicitationResult, 1),
	}
}

// reapPendingAskUser 在工具卡进入终态时清理未被 elicitation 绑定的条目 ——
// 「工具失败但没走 elicitation」的提问会一直留在表里。
func (p *Process) reapPendingAskUser(callID, status string) {
	switch status {
	case "completed", "failed":
	default:
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	p.elicitationMu.Lock()
	defer p.elicitationMu.Unlock()
	entry, ok := p.pendingAskUserByCallID[callID]
	if !ok || entry.bound {
		return
	}
	delete(p.pendingAskUserByCallID, callID)
}

// bindElicitation 把一次 elicitation/create 关联到一张在等的 ask_user 卡。
//
// 关联键是题目 id + 题目文本（两边逐字相同）。
//
// 刻意**不做**「只剩一张卡就绑它」的兜底：并发提问时，A 的卡已登记、B 的 elicitation
// 先到，兜底会把 B 的答案投给 A —— 答错题比答不上更糟。等待窗口已经覆盖了
// 「tool_call 通知还在 SDK 队列里」这个真正的时序问题；窗口内等不到就返回 nil，
// 由调用方回 Decline（fail-safe，agent 收到的是「用户取消提问」）。
func (p *Process) bindElicitation(questions []elicitationQuestion) *pendingAskUser {
	deadline := time.Now().Add(elicitationBindWait)
	for {
		if entry := p.matchAndBindPendingAskUser(questions); entry != nil {
			return entry
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(elicitationBindPoll)
	}
}

// matchAndBindPendingAskUser 在**同一把锁**里完成匹配与占位。
//
// 拆成「先匹配、再置 bound」两步会让两个并发的 elicitation（不同会话问了同一道题）
// 同时匹配到同一条目、一起等同一个 waiter —— 一个拿到答案，另一个只能等到 ctx 取消。
func (p *Process) matchAndBindPendingAskUser(questions []elicitationQuestion) *pendingAskUser {
	p.elicitationMu.Lock()
	defer p.elicitationMu.Unlock()
	for _, entry := range p.pendingAskUserByCallID {
		if entry.bound {
			continue
		}
		if elicitationQuestionsMatch(entry.questions, questions) {
			entry.bound = true
			return entry
		}
	}
	return nil
}

func elicitationQuestionsMatch(a, b []elicitationQuestion) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Question != b[i].Question {
			return false
		}
	}
	return true
}

// AnswerElicitation 把前端提交的答案编码成 elicitation content，投递给挂起的提问。
func (p *Process) AnswerElicitation(ctx context.Context, callID string, answers map[string]string) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("toolUseId required")
	}
	normalized := make(map[string]string, len(answers))
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			normalized[key] = value
		}
	}
	if len(normalized) == 0 {
		return errors.New("answers required")
	}

	p.elicitationMu.Lock()
	entry, ok := p.pendingAskUserByCallID[callID]
	if ok {
		delete(p.pendingAskUserByCallID, callID)
	}
	p.elicitationMu.Unlock()
	if !ok {
		return fmt.Errorf("question is not pending: %s", callID)
	}

	select {
	case entry.waiter <- elicitationResult{content: elicitationContent(entry.questions, normalized)}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// cancelPendingAskUser 把某会话名下所有挂起的提问按「用户取消」回给 agent。
func (p *Process) cancelPendingAskUser(sessionKey string) {
	p.elicitationMu.Lock()
	defer p.elicitationMu.Unlock()
	for callID, entry := range p.pendingAskUserByCallID {
		if entry.sessionKey != sessionKey {
			continue
		}
		delete(p.pendingAskUserByCallID, callID)
		entry.waiter <- elicitationResult{}
	}
}

// dropAllPendingAskUser 在进程收尾时清空所有挂起的提问，避免 goroutine 泄漏。
func (p *Process) dropAllPendingAskUser() {
	p.elicitationMu.Lock()
	defer p.elicitationMu.Unlock()
	for callID, entry := range p.pendingAskUserByCallID {
		delete(p.pendingAskUserByCallID, callID)
		entry.waiter <- elicitationResult{}
	}
}

// acpAskUserQuestions 从 ACP 工具卡的 rawInput 里认出 dsh 的 ask_user_question。
//
// 判定只看结构（questions 是非空数组、每项有 question），不看 title/kind ——
// 适配器把 ask_user_question 归成 facts("other","ask_user_question")，title 就是工具名，
// 但 presentCall 在 mindfs-acp profile 里没接线，所以 title 稳定；不依赖它更稳。
func acpAskUserQuestions(rawInput any) []elicitationQuestion {
	data, ok := normalizeACPValue(rawInput).(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := data["questions"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	questions := make([]elicitationQuestion, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			return nil
		}
		question, _ := item["question"].(string)
		if strings.TrimSpace(question) == "" {
			return nil
		}
		id, _ := item["id"].(string)
		header, _ := item["header"].(string)
		var options []string
		if rawOptions, ok := item["options"].([]any); ok {
			for _, rawOption := range rawOptions {
				option, ok := rawOption.(map[string]any)
				if !ok {
					continue
				}
				label, _ := option["label"].(string)
				if strings.TrimSpace(label) == "" {
					continue
				}
				options = append(options, label)
			}
		}
		multiSelect, _ := item["multi_select"].(bool)
		questions = append(questions, elicitationQuestion{
			ID:          strings.TrimSpace(id),
			Question:    strings.TrimSpace(question),
			Header:      strings.TrimSpace(header),
			Options:     options,
			MultiSelect: multiSelect,
		})
	}
	return questions
}

// dshElicitationQuestions 解析 openma 适配器在 _meta["dsh.userQuestions"] 里带的题目。
func dshElicitationQuestions(meta map[string]any) []elicitationQuestion {
	raw, ok := meta[dshUserQuestionsMetaKey]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var parsed dshUserQuestionsPayload
	if err := json.Unmarshal(encoded, &parsed); err != nil || len(parsed.Questions) == 0 {
		return nil
	}
	questions := make([]elicitationQuestion, 0, len(parsed.Questions))
	for _, item := range parsed.Questions {
		question := strings.TrimSpace(item.Question)
		if question == "" {
			return nil
		}
		options := make([]string, 0, len(item.Options))
		for _, option := range item.Options {
			if label := strings.TrimSpace(option.Label); label != "" {
				options = append(options, label)
			}
		}
		questions = append(questions, elicitationQuestion{
			ID:          strings.TrimSpace(item.ID),
			Question:    question,
			Header:      strings.TrimSpace(item.Header),
			Options:     options,
			MultiSelect: item.MultiSelect,
		})
	}
	return questions
}

// acpAskUserToolCall 判断一张 ACP 工具卡是不是 dsh 的 ask_user_question。
// 是则返回重标后的 kind、给前端的 meta、以及内部题目形状。
func acpAskUserToolCall(rawInput any) (types.ToolKind, map[string]any, []elicitationQuestion) {
	questions := acpAskUserQuestions(rawInput)
	if len(questions) == 0 {
		return "", nil, nil
	}
	return types.ToolKindAskUser, map[string]any{"questions": askUserQuestionItems(questions)}, questions
}

// askUserQuestionItems 把内部题目形状转成前端认的 meta.questions。
func askUserQuestionItems(questions []elicitationQuestion) []types.AskUserQuestionItem {
	items := make([]types.AskUserQuestionItem, 0, len(questions))
	for _, question := range questions {
		options := make([]types.AskUserQuestionOption, 0, len(question.Options))
		for _, label := range question.Options {
			options = append(options, types.AskUserQuestionOption{Label: label})
		}
		items = append(items, types.AskUserQuestionItem{
			Question:    question.Question,
			Header:      question.Header,
			Options:     options,
			MultiSelect: question.MultiSelect,
		})
	}
	return items
}

// elicitationContent 把前端的 q_<i> 答案编码成 openma 适配器认的 elicitation content。
//
// 前端约定（SessionViewer）：q_<i> = 选中的 option label，多选用 ", " 连接，
// 无选项或走自定义输入时 = 自由文本。
// 适配器约定（bridge.js 的 answerFromElicitation）：
//   - question_<i> = option_<j>（j 是该 label 在 options 里的下标），多选是数组；
//   - question_<i>_custom = 自由文本，单选时非空会覆盖掉选项选择；
//   - 无选项的题 question_<i> 直接是字符串。
func elicitationContent(questions []elicitationQuestion, answers map[string]string) map[string]any {
	content := make(map[string]any, len(questions))
	for index, question := range questions {
		raw := strings.TrimSpace(answers[fmt.Sprintf("q_%d", index)])
		if raw == "" {
			continue
		}
		field := fmt.Sprintf("question_%d", index)
		if !question.hasOptions() {
			content[field] = raw
			continue
		}
		if !question.MultiSelect {
			if option := optionIndex(question.Options, raw); option >= 0 {
				content[field] = optionValue(option)
				continue
			}
			content[customField(index)] = raw
			continue
		}
		selected := make([]string, 0, 1)
		custom := ""
		for _, label := range strings.Split(raw, ", ") {
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			if option := optionIndex(question.Options, label); option >= 0 {
				selected = append(selected, optionValue(option))
				continue
			}
			custom = label
		}
		content[field] = selected
		if custom != "" {
			content[customField(index)] = custom
		}
	}
	return content
}

func optionIndex(options []string, label string) int {
	for index, option := range options {
		if option == label {
			return index
		}
	}
	return -1
}

func optionValue(index int) string {
	return fmt.Sprintf("option_%d", index)
}

func customField(index int) string {
	return fmt.Sprintf("question_%d_custom", index)
}

// logElicitation 统一前缀，便于在日志里把一次提问的四个阶段串起来。
func (p *Process) logElicitation(stage, sessionKey, callID string, extra ...any) {
	if p == nil {
		return
	}
	log.Printf("[agent/acp] elicitation.%s agent=%s session_key=%s call_id=%s %v",
		stage, p.agentLabel(), sessionKey, callID, extra)
}
