package acp

import (
	"context"
	"strings"
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
