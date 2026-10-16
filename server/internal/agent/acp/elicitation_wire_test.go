package acp

import (
	"encoding/json"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestDshElicitationQuestionsParsesMeta(t *testing.T) {
	meta := map[string]any{
		dshUserQuestionsMetaKey: map[string]any{
			"version": 1,
			"questions": []any{
				map[string]any{
					// 驼峰：_meta 里的题目来自 user-questions seam，不是工具参数的蛇形。
					"id":          "confirm",
					"question":    "继续吗？",
					"header":      "确认",
					"multiSelect": true,
					"options": []any{
						map[string]any{"label": "继续", "description": "按当前方案执行"},
						map[string]any{"label": "停止"},
					},
				},
			},
		},
	}

	questions := dshElicitationQuestions(meta)
	if len(questions) != 1 {
		t.Fatalf("questions = %#v, want one", questions)
	}
	if questions[0].ID != "confirm" || questions[0].Question != "继续吗？" || questions[0].Header != "确认" {
		t.Fatalf("question = %#v", questions[0])
	}
	if !questions[0].MultiSelect {
		t.Fatal("multiSelect = false, want true（_meta 里是驼峰 multiSelect）")
	}
	if len(questions[0].Options) != 2 || questions[0].Options[0] != "继续" || questions[0].Options[1] != "停止" {
		t.Fatalf("options = %#v", questions[0].Options)
	}

	if got := dshElicitationQuestions(map[string]any{}); got != nil {
		t.Fatalf("missing meta = %#v, want nil", got)
	}
	if got := dshElicitationQuestions(map[string]any{dshUserQuestionsMetaKey: map[string]any{"questions": []any{}}}); got != nil {
		t.Fatalf("empty questions = %#v, want nil", got)
	}
}

// TestAskUserRawInputAndElicitationMetaAgree 钉住两个来源必须归一化成同一形状：
// 工具卡的 rawInput 是工具参数的**蛇形**（multi_select），elicitation 的 _meta 是
// user-questions seam 的**驼峰**（multiSelect）。两边都来自同一次模型输出，
// 归一化后必须逐字相等 —— 否则 elicitationQuestionsMatch 永远匹配不上，
// 提问会静默退化成「用户取消」。
func TestAskUserRawInputAndElicitationMetaAgree(t *testing.T) {
	rawInput := acpAskUserRawInput(
		map[string]any{
			"id":       "confirm",
			"question": "继续吗？",
			"header":   "确认",
			"options": []any{
				map[string]any{"label": "继续", "description": "按当前方案执行"},
				map[string]any{"label": "停止"},
			},
			"multi_select": true,
		},
		map[string]any{"id": "free", "question": "还有别的吗？"},
	)
	meta := map[string]any{
		dshUserQuestionsMetaKey: map[string]any{
			"version": 1,
			"questions": []any{
				map[string]any{
					"id":          "confirm",
					"question":    "继续吗？",
					"header":      "确认",
					"multiSelect": true,
					"options": []any{
						map[string]any{"label": "继续", "description": "按当前方案执行"},
						map[string]any{"label": "停止"},
					},
				},
				map[string]any{"id": "free", "question": "还有别的吗？"},
			},
		},
	}

	fromToolCall := acpAskUserQuestions(rawInput)
	fromMeta := dshElicitationQuestions(meta)
	if len(fromToolCall) != 2 || len(fromMeta) != 2 {
		t.Fatalf("fromToolCall = %#v, fromMeta = %#v", fromToolCall, fromMeta)
	}
	if !elicitationQuestionsMatch(fromToolCall, fromMeta) {
		t.Fatalf("questions do not match:\n  toolCall = %#v\n  meta     = %#v", fromToolCall, fromMeta)
	}
	if !fromMeta[0].MultiSelect {
		t.Fatalf("fromMeta[0].MultiSelect = false, want true（驼峰没被读到？）")
	}
	if fromMeta[1].hasOptions() {
		t.Fatalf("fromMeta[1] = %#v, want no options", fromMeta[1])
	}

	// 多选要编码成数组：读不到 multiSelect 会退化成字符串，适配器只认第一个。
	content := elicitationContent(fromMeta, map[string]string{"q_0": "继续, 停止", "q_1": "没有"})
	selected, ok := content["question_0"].([]string)
	if !ok || len(selected) != 2 {
		t.Fatalf("content[question_0] = %#v, want two-element array", content["question_0"])
	}
	if content["question_1"] != "没有" {
		t.Fatalf("content[question_1] = %#v, want 没有", content["question_1"])
	}
}

// TestElicitationWireShape 钉住与 openma 适配器之间的**协议字面量**。
//
// 单测覆盖了逻辑，但这条通路整体是一份 JSON 契约：能力字段名少一个字母，适配器
// 就认为客户端不支持（bridge.js 判的是 `clientCapabilities?.elicitation?.form`）；
// 响应少一个 action 判别式，适配器的 answerFromElicitation 直接返回 undefined。
// 这些都不需要连真的 dsh 就能验，且比连真的更能定位。
func TestElicitationWireShape(t *testing.T) {
	t.Run("client capabilities", func(t *testing.T) {
		caps := acp.ClientCapabilities{
			Terminal:    false,
			Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}},
		}
		encoded, err := json.Marshal(caps)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded struct {
			Elicitation *struct {
				Form *map[string]any `json:"form"`
			} `json:"elicitation"`
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.Elicitation == nil || decoded.Elicitation.Form == nil {
			t.Fatalf("capabilities = %s, want elicitation.form present（适配器只认这个）", encoded)
		}
	})

	t.Run("inbound create request", func(t *testing.T) {
		// 形状照抄 openma 的 questionsToElicitation 输出。
		params := []byte(`{
			"mode": "form",
			"sessionId": "session-1",
			"message": "The agent needs your input.",
			"requestedSchema": {"type": "object", "properties": {}, "required": []},
			"_meta": {"dsh.userQuestions": {"version": 1, "questions": [
				{"id": "confirm", "question": "继续吗？", "header": "确认",
				 "options": [{"label": "继续", "description": "按当前方案执行"}, {"label": "停止"}],
				 "multiSelect": true}
			]}}
		}`)
		var req acp.UnstableCreateElicitationRequest
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if err := req.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if req.Form == nil {
			t.Fatalf("request = %#v, want Form variant（判别式是 mode=form）", req)
		}
		if req.Form.Mode != "form" {
			t.Fatalf("mode = %q, want form", req.Form.Mode)
		}
		questions := dshElicitationQuestions(req.Form.Meta)
		if len(questions) != 1 {
			t.Fatalf("questions = %#v, want one（_meta 没被 SDK 吞掉？）", questions)
		}
		if !questions[0].MultiSelect || len(questions[0].Options) != 2 {
			t.Fatalf("question = %#v", questions[0])
		}
	})

	t.Run("outbound responses", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			resp acp.UnstableCreateElicitationResponse
			want string
		}{
			{
				name: "accept",
				resp: acp.UnstableCreateElicitationResponse{
					Accept: &acp.UnstableCreateElicitationAccept{Content: map[string]any{"question_0": "option_1"}},
				},
				want: `{"action":"accept","content":{"question_0":"option_1"}}`,
			},
			{
				name: "decline",
				resp: acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{}},
				want: `{"action":"decline"}`,
			},
			{
				name: "cancel",
				resp: acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{}},
				want: `{"action":"cancel"}`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				encoded, err := json.Marshal(tc.resp)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if string(encoded) != tc.want {
					t.Fatalf("response = %s, want %s", encoded, tc.want)
				}
				if err := tc.resp.Validate(); err != nil {
					t.Fatalf("validate: %v", err)
				}
			})
		}
	})
}

// TestACPClientCapabilitiesOnlyAdvertisesElicitationForDsh 钉住兼容性边界：
// 只有 dsh 拿到 elicitation.form。给别的 ACP agent 广告，会让它们开始发
// elicitation，而 mindfs 只能 decline —— 等于把「本来就不支持」变成「支持但总是失败」。
func TestACPClientCapabilitiesOnlyAdvertisesElicitationForDsh(t *testing.T) {
	for _, tc := range []struct {
		agent string
		want  bool
	}{
		{agent: "dsh", want: true},
		{agent: "gemini", want: false},
		{agent: "copilot", want: false},
		{agent: "qwen", want: false},
		{agent: "CodeBuddy", want: false},
		{agent: "reasonix", want: false},
		{agent: "", want: false},
		{agent: "DSH", want: false}, // 大小写敏感，agent 名来自 agents.json 的 name
	} {
		t.Run(tc.agent, func(t *testing.T) {
			caps := acpClientCapabilities(tc.agent)
			if got := caps.Elicitation != nil; got != tc.want {
				t.Fatalf("agent %q: Elicitation present = %v, want %v", tc.agent, got, tc.want)
			}
			if caps.Terminal {
				t.Fatalf("agent %q: Terminal = true, want false（上游行为）", tc.agent)
			}
		})
	}
}
