package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Render builds the card as Feishu sees it (schema 2.0). A card changes only
// through Render, so the same Card always produces the same message — that is
// what makes "already rendered at this version" checkable.
func Render(c Card) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true, "enable_forward": false},
		"header": map[string]any{
			"title":    plainText(c.title()),
			"template": template(c),
		},
		"body": map[string]any{"elements": elements(c)},
	}
}

// RenderJSON encodes Render for lark-cli's --content / patch body.
func RenderJSON(c Card) (string, error) {
	raw, err := json.Marshal(Render(c))
	if err != nil {
		return "", fmt.Errorf("render card %s: %w", c.ID, err)
	}
	return string(raw), nil
}

func (c Card) title() string {
	switch {
	case c.Kind == KindTarget:
		label := ""
		if c.Target != nil {
			label = c.Target.ProjectLabel
		}
		if label == "" {
			return "确认目标"
		}
		return "确认目标：" + label
	case c.Kind == KindPicker:
		question := c.IntakeText
		if question == "" {
			question = c.Prompt
		}
		if question == "" {
			return "请选择项目"
		}
		runes := []rune(question)
		if len(runes) > 50 {
			return "选择项目：" + string(runes[:50]) + "…"
		}
		return "选择项目：" + question
	case c.TaskTitle != "":
		return c.TaskTitle
	case c.Prompt != "":
		return c.Prompt
	default:
		return "MindFS"
	}
}

func template(c Card) string {
	switch c.State {
	case StateOpen:
		return "blue"
	case StateApplying:
		return "orange"
	case StateFailed, StateExpired:
		return "grey"
	default:
		return "green"
	}
}

func elements(c Card) []any {
	if c.Kind == KindPicker {
		if c.State != StateOpen {
			return []any{markdown("**" + outcome(c) + "**")}
		}
		return pickerBlocks(c)
	}
	out := make([]any, 0, 6)
	if c.Prompt != "" && c.Prompt != c.title() {
		out = append(out, markdown(c.Prompt))
	}
	if c.Text != "" {
		out = append(out, markdown(c.Text))
	}
	if c.State == StateOpen && c.LastError != "" {
		// An open card stays tappable; the owner must see why the last answer
		// did not reach the worker before clicking again.
		out = append(out, plainDiv("回传失败："+c.LastError))
	}
	if c.State == StateOpen && c.Kind == KindTarget && len(c.ReviewHits) > 0 {
		// Confirming here means the project will run an approval workflow; say
		// so before the owner clicks, not after.
		out = append(out, plainDiv("该项目需要审核："+strings.Join(c.ReviewHits, "、")))
	}
		if c.State == StateOpen {
			if c.Kind == KindGate {
				return append(out, optionBlocks(c)...)
			}
			if c.Kind == KindNotice {
				out = append(out, textForm(c))
				out = append(out, optionBlocks(c)...)
				return out
			}
			if c.Kind == KindTarget {
				out = append(out, optionBlocks(c)...)
				out = append(out, targetTaskForm(c))
				return out
			}
			out = append(out, optionBlocks(c)...)
			out = append(out, textForm(c))
			return out
		}
	out = append(out, markdown("**"+outcome(c)+"**"))
	if c.Note != "" {
		out = append(out, plainDiv("处理说明："+c.Note))
	}
	return out
}

// optionBlocks renders each choice as a full-width tappable block. Buttons in
// a row cut long labels off; blocks wrap.
func optionBlocks(c Card) []any {
	out := make([]any, 0, len(c.Choices))
	for i, ch := range c.Choices {
		out = append(out, map[string]any{
			"tag":           "interactive_container",
			"width":         "fill",
			"has_border":    true,
			"border_color":  choiceBorder(c, i),
			"corner_radius": "8px",
			"padding":       "8px 12px 8px 12px",
			"behaviors": []any{map[string]any{
				"type":  "callback",
				"value": map[string]any{"card": c.ID, "choice": i},
			}},
			"elements": []any{plainDiv(ch.Label)},
		})
	}
	return out
}

// pickerBlocks reuses the ask card's tappable blocks without its free-text
// input: each index is a project candidate, in the order the owner sees.
func pickerBlocks(c Card) []any {
	picker := c
	picker.Choices = make([]Choice, 0, len(c.Projects))
	for _, project := range c.Projects {
		label := project.Label
		if label == "" {
			label = project.RootID
		}
		picker.Choices = append(picker.Choices, Choice{Label: label})
	}
	return optionBlocks(picker)
}

func choiceBorder(c Card, i int) string {
	if c.Selected == i {
		return "blue"
	}
	return "grey"
}

// textForm is the free-text escape hatch (an ask answered in words, a notice
// carrying how it was handled). Its submit button cannot carry a value, so
// the card ID lives in the button's name.
func textForm(c Card) any {
	name, submit, label, placeholder, style := "ask_text_", "提交文字", "选项都不合适，就直接写回答",
		"写好点“提交文字”；写了再点选项，文字不会带上", "default"
	if c.Kind == KindNotice {
		name, submit, label, placeholder, style = "handling_note_", "记录并结案", "处理说明（可不填）",
			"比如：已回复他视频下周发；或：群发广告，不用管", "primary"
	}
	return map[string]any{
		"tag":  "form",
		"name": name + c.ID,
		"elements": []any{
			map[string]any{
				"tag":            "input",
				"name":           "note",
				"input_type":     "multiline_text",
				"rows":           2,
				"auto_resize":    true,
				"max_length":     1000,
				"label":          plainText(label),
				"label_position": "top",
				"placeholder":    plainText(placeholder),
			},
			map[string]any{
				"tag":              "button",
				"name":             name + "submit_" + c.ID,
				"form_action_type": "submit",
				"type":             style,
				"text":             plainText(submit),
			},
		},
	}
}

// targetTaskForm is the editable task number on a confirmation card. Submitting
// it means the same as 「确认」 with that task: a number the card knows switches
// the target, an empty input keeps whatever was prefilled.
func targetTaskForm(c Card) any {
	value := ""
	if c.Target != nil && c.Target.TaskNumber > 0 {
		value = fmt.Sprintf("#%d", c.Target.TaskNumber)
	}
	input := map[string]any{
		"tag":            "input",
		"name":           "task_no",
		"input_type":     "text",
		"max_length":     16,
		"label":          plainText("任务号（可选，填了就追加到该任务）"),
		"label_position": "top",
		"placeholder":    plainText("#42"),
	}
	if value != "" {
		input["default_value"] = value
	}
	return map[string]any{
		"tag":  "form",
		"name": "target_form_" + c.ID,
		"elements": []any{
			input,
			map[string]any{
				"tag":              "button",
				"name":             "target_form_submit_" + c.ID,
				"form_action_type": "submit",
				"type":             "primary",
				"text":             plainText("按任务号确认"),
			},
		},
	}
}

func outcome(c Card) string {
	chosen := c.Chosen()
	switch c.State {
	case StateApplying:
		return "已收到你的点击，正在处理…"
	case StateDone:
		if c.Summary != "" {
			return c.Summary
		}
		if chosen != "" {
			return "你选了「" + chosen + "」，已执行。"
		}
		return "已处理。"
	case StateFailed:
		if c.Summary != "" {
			return c.Summary
		}
		return "处理失败，请到 MindFS 网页里完成。"
	case StateExpired:
		return "这张卡已过期，请到 MindFS 网页里处理。"
	case StateSuperseded:
		return "这张卡已被新的一轮取代，请到 MindFS 网页里查看。"
	default:
		return "已处理。"
	}
}

func markdown(text string) map[string]any {
	return map[string]any{"tag": "markdown", "content": text}
}

func plainDiv(text string) map[string]any {
	return map[string]any{"tag": "div", "text": plainText(text)}
}

func plainText(text string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": text}
}
