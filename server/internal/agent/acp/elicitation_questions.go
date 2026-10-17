package acp

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"
)

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
