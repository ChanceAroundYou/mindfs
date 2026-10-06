package acp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func newElicitationTestProcess() *Process {
	return &Process{agentName: "dsh", pendingAskUserByCallID: make(map[string]*pendingAskUser)}
}

// shortenElicitationBindWait 把关联等待窗口调短，避免「等不到就放弃」的用例真等 2s。
func shortenElicitationBindWait(t *testing.T) {
	t.Helper()
	original := elicitationBindWait
	elicitationBindWait = 500 * time.Millisecond
	t.Cleanup(func() { elicitationBindWait = original })
}

func TestElicitationContentMapsLabelsToOptionIndexes(t *testing.T) {
	questions := []elicitationQuestion{
		{ID: "single", Question: "继续吗？", Options: []string{"继续", "停止"}},
		{ID: "multi", Question: "选哪些？", Options: []string{"快速", "完整", "自定义"}, MultiSelect: true},
		{ID: "free", Question: "想说什么？"},
	}

	for _, tc := range []struct {
		name    string
		answers map[string]string
		want    map[string]any
	}{
		{
			name:    "single select hit",
			answers: map[string]string{"q_0": "停止"},
			want:    map[string]any{"question_0": "option_1"},
		},
		{
			name:    "single select custom",
			answers: map[string]string{"q_0": "先停一下"},
			want:    map[string]any{"question_0_custom": "先停一下"},
		},
		{
			name:    "multi select hit",
			answers: map[string]string{"q_1": "快速, 完整"},
			want:    map[string]any{"question_1": []string{"option_0", "option_1"}},
		},
		{
			name:    "multi select mixed with custom",
			answers: map[string]string{"q_1": "快速, 其他"},
			want:    map[string]any{"question_1": []string{"option_0"}, "question_1_custom": "其他"},
		},
		{
			name:    "free text",
			answers: map[string]string{"q_2": "随便写"},
			want:    map[string]any{"question_2": "随便写"},
		},
		{
			name:    "blank answer skipped",
			answers: map[string]string{"q_0": "   "},
			want:    map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := elicitationContent(questions, tc.answers)
			if len(got) != len(tc.want) {
				t.Fatalf("content = %#v, want %#v", got, tc.want)
			}
			for key, want := range tc.want {
				gotValue, ok := got[key]
				if !ok {
					t.Fatalf("content[%q] missing, want %#v", key, want)
				}
				if !elicitationContentEqual(gotValue, want) {
					t.Fatalf("content[%q] = %#v, want %#v", key, gotValue, want)
				}
			}
		})
	}
}

func elicitationContentEqual(got, want any) bool {
	switch wantValue := want.(type) {
	case string:
		gotValue, ok := got.(string)
		return ok && gotValue == wantValue
	case []string:
		gotValue, ok := got.([]string)
		if !ok || len(gotValue) != len(wantValue) {
			return false
		}
		for i := range wantValue {
			if gotValue[i] != wantValue[i] {
				return false
			}
		}
		return true
	}
	return false
}

func TestBindElicitationMatchesByQuestionIDs(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	proc.registerPendingAskUser("call-a", "session-a", []elicitationQuestion{
		{ID: "confirm", Question: "继续吗？", Options: []string{"继续"}},
	})
	proc.registerPendingAskUser("call-b", "session-b", []elicitationQuestion{
		{ID: "mode", Question: "选哪种模式？", Options: []string{"快速"}},
	})

	entry := proc.bindElicitation([]elicitationQuestion{
		{ID: "mode", Question: "选哪种模式？", Options: []string{"快速"}},
	})
	if entry == nil {
		t.Fatal("bindElicitation = nil, want call-b")
	}
	if entry.callID != "call-b" {
		t.Fatalf("callID = %q, want call-b", entry.callID)
	}
	if !entry.bound {
		t.Fatal("entry.bound = false, want true")
	}

	// 已绑定的条目不能再被第二次绑定。
	if again := proc.bindElicitation([]elicitationQuestion{
		{ID: "mode", Question: "选哪种模式？", Options: []string{"快速"}},
	}); again != nil {
		t.Fatalf("second bind = %q, want nil", again.callID)
	}
}

func TestBindElicitationWaitsForLateToolCall(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？"}}

	bound := make(chan *pendingAskUser, 1)
	go func() {
		bound <- proc.bindElicitation(questions)
	}()

	// 模拟 SDK 的 notification 队列：elicitation 先到，tool_call 通知晚一点才落地。
	time.Sleep(60 * time.Millisecond)
	proc.registerPendingAskUser("call-late", "session-a", questions)

	select {
	case entry := <-bound:
		if entry == nil || entry.callID != "call-late" {
			t.Fatalf("bind = %v, want call-late", entry)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bindElicitation did not pick up the late tool call")
	}
}

func TestBindElicitationGivesUpWhenNothingMatches(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	proc.registerPendingAskUser("call-a", "session-a", []elicitationQuestion{
		{ID: "confirm", Question: "继续吗？"},
	})

	// 题目对不上，且等待窗口内没有新的工具卡出现 → 放弃，由调用方回 Decline。
	// 刻意不做「只剩一张就绑它」的兜底：并发提问时那会把答案投给错误的题目。
	entry := proc.bindElicitation([]elicitationQuestion{{ID: "other", Question: "另一题？"}})
	if entry != nil {
		t.Fatalf("bindElicitation = %q, want nil", entry.callID)
	}
}

func TestAnswerElicitationDeliversContent(t *testing.T) {
	proc := newElicitationTestProcess()
	proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{
		{ID: "confirm", Question: "继续吗？", Options: []string{"继续", "停止"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := proc.AnswerElicitation(ctx, "call-1", map[string]string{"q_0": "继续"}); err != nil {
		t.Fatalf("AnswerElicitation error = %v", err)
	}

	proc.elicitationMu.Lock()
	_, stillPending := proc.pendingAskUserByCallID["call-1"]
	proc.elicitationMu.Unlock()
	if stillPending {
		t.Fatal("entry still pending after answer")
	}
}

func TestAnswerElicitationRejectsUnknownCall(t *testing.T) {
	proc := newElicitationTestProcess()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := proc.AnswerElicitation(ctx, "call-missing", map[string]string{"q_0": "继续"})
	if err == nil || !strings.Contains(err.Error(), "call-missing") {
		t.Fatalf("error = %v, want question is not pending: call-missing", err)
	}
}

func TestCancelAndCloseDrainPendingQuestions(t *testing.T) {
	proc := newElicitationTestProcess()
	proc.registerPendingAskUser("call-a", "session-a", []elicitationQuestion{{ID: "a", Question: "甲？"}})
	proc.registerPendingAskUser("call-b", "session-b", []elicitationQuestion{{ID: "b", Question: "乙？"}})

	proc.cancelPendingAskUser("session-a")

	proc.elicitationMu.Lock()
	_, aStillPending := proc.pendingAskUserByCallID["call-a"]
	bStillPending := proc.pendingAskUserByCallID["call-b"] != nil
	proc.elicitationMu.Unlock()
	if aStillPending {
		t.Fatal("call-a still pending after cancelPendingAskUser")
	}
	if !bStillPending {
		t.Fatal("call-b dropped, want kept (只取消指定会话)")
	}

	// 取消后 waiter 已经收到空 content，阻塞的 handler 会走 Decline。
	proc.dropAllPendingAskUser()
	proc.elicitationMu.Lock()
	remaining := len(proc.pendingAskUserByCallID)
	proc.elicitationMu.Unlock()
	if remaining != 0 {
		t.Fatalf("pending = %d, want 0", remaining)
	}
}

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

func TestUnstableCreateElicitationAnswersPendingQuestion(t *testing.T) {
	proc := newElicitationTestProcess()
	client := &mindfsClient{proc: proc}
	proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{
		{ID: "confirm", Question: "继续吗？", Options: []string{"继续", "停止"}},
	})

	meta := map[string]any{
		dshUserQuestionsMetaKey: map[string]any{
			"version": 1,
			"questions": []any{
				map[string]any{
					"id":       "confirm",
					"question": "继续吗？",
					"options":  []any{map[string]any{"label": "继续"}, map[string]any{"label": "停止"}},
				},
			},
		},
	}
	req := acp.UnstableCreateElicitationRequest{
		Form: &acp.UnstableCreateElicitationForm{
			Mode:    "form",
			Message: "The agent needs your input.",
			Meta:    meta,
		},
	}

	var (
		resp acp.UnstableCreateElicitationResponse
		err  error
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		resp, err = client.UnstableCreateElicitation(context.Background(), req)
	}()

	// 等 handler 绑定上再答，避免答案抢在绑定之前。
	time.Sleep(80 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if answerErr := proc.AnswerElicitation(ctx, "call-1", map[string]string{"q_0": "停止"}); answerErr != nil {
		t.Fatalf("AnswerElicitation error = %v", answerErr)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UnstableCreateElicitation did not return")
	}
	if err != nil {
		t.Fatalf("UnstableCreateElicitation error = %v", err)
	}
	if resp.Accept == nil {
		t.Fatalf("response = %#v, want Accept", resp)
	}
	if got := resp.Accept.Content["question_0"]; got != "option_1" {
		t.Fatalf("content[question_0] = %#v, want option_1", got)
	}
}

func TestUnstableCreateElicitationDeclinesUnsupported(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	client := &mindfsClient{proc: proc}

	t.Run("non form mode", func(t *testing.T) {
		resp, err := client.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{
			Url: &acp.UnstableCreateElicitationUrl{Mode: "url", ElicitationId: "e1", Url: "https://example.com"},
		})
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if resp.Decline == nil {
			t.Fatalf("response = %#v, want Decline", resp)
		}
	})

	t.Run("missing questions", func(t *testing.T) {
		resp, err := client.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{
			Form: &acp.UnstableCreateElicitationForm{Mode: "form", Message: "需要输入"},
		})
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if resp.Decline == nil {
			t.Fatalf("response = %#v, want Decline", resp)
		}
	})

	t.Run("no pending card", func(t *testing.T) {
		resp, err := client.UnstableCreateElicitation(context.Background(), acp.UnstableCreateElicitationRequest{
			Form: &acp.UnstableCreateElicitationForm{
				Mode: "form",
				Meta: map[string]any{
					dshUserQuestionsMetaKey: map[string]any{
						"version": 1,
						"questions": []any{
							map[string]any{"id": "confirm", "question": "继续吗？"},
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if resp.Decline == nil {
			t.Fatalf("response = %#v, want Decline", resp)
		}
	})
}

func TestUnstableCreateElicitationCancelOnContextDone(t *testing.T) {
	proc := newElicitationTestProcess()
	client := &mindfsClient{proc: proc}
	proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{
		{ID: "confirm", Question: "继续吗？"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan acp.UnstableCreateElicitationResponse, 1)
	go func() {
		resp, err := client.UnstableCreateElicitation(ctx, acp.UnstableCreateElicitationRequest{
			Form: &acp.UnstableCreateElicitationForm{
				Mode: "form",
				Meta: map[string]any{
					dshUserQuestionsMetaKey: map[string]any{
						"version": 1,
						"questions": []any{
							map[string]any{"id": "confirm", "question": "继续吗？"},
						},
					},
				},
			},
		})
		if err == nil {
			done <- resp
		}
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case resp := <-done:
		if resp.Cancel == nil {
			t.Fatalf("response = %#v, want Cancel", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UnstableCreateElicitation did not return after cancel")
	}
}

// TestPendingAskUserRegistryConcurrency 守住「同一 callID 只登记一次」与
// 「删除与投递互斥」这两条不变量 —— waiter 容量 1 且每条目只投递一次，
// 靠 elicitationMu 保证，一旦破掉就是 panic 或永久阻塞。
func TestPendingAskUserRegistryConcurrency(t *testing.T) {
	proc := newElicitationTestProcess()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{{ID: "a", Question: "甲？"}})
			proc.reapPendingAskUser("call-1", "completed")
			proc.cancelPendingAskUser("session-a")
			proc.dropAllPendingAskUser()
		}(i)
	}
	wg.Wait()

	proc.elicitationMu.Lock()
	defer proc.elicitationMu.Unlock()
	if len(proc.pendingAskUserByCallID) != 0 {
		t.Fatalf("pending = %d, want 0", len(proc.pendingAskUserByCallID))
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

// TestBindElicitationIsExclusiveUnderConcurrency 钉住「匹配与占位必须原子」：
// 两个并发的 elicitation（不同会话问了同一道题）不能绑到同一条目，
// 否则一个拿到答案、另一个只能干等到 ctx 取消。
func TestBindElicitationIsExclusiveUnderConcurrency(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？", Options: []string{"继续"}}}
	for _, callID := range []string{"call-a", "call-b", "call-c", "call-d"} {
		proc.registerPendingAskUser(callID, "session-"+callID, questions)
	}

	const workers = 8
	results := make(chan *pendingAskUser, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- proc.bindElicitation(questions)
		}()
	}
	wg.Wait()
	close(results)

	bound := map[string]int{}
	for entry := range results {
		if entry != nil {
			bound[entry.callID]++
		}
	}
	if len(bound) != 4 {
		t.Fatalf("bound = %#v, want 4 distinct entries（每个条目最多被绑一次）", bound)
	}
	for callID, count := range bound {
		if count != 1 {
			t.Fatalf("%s bound %d times, want 1", callID, count)
		}
	}
}
