package acp

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	types "mindfs/server/internal/agent/types"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestIsExpectedStreamCloseError(t *testing.T) {
	for _, err := range []error{nil, os.ErrClosed, io.ErrClosedPipe, &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}} {
		if !isExpectedStreamCloseError(err) {
			t.Fatalf("error %v should be treated as an expected stream close", err)
		}
	}
	if isExpectedStreamCloseError(errors.New("unexpected read failure")) {
		t.Fatal("unexpected read failure was suppressed")
	}
}

func TestACPDSHTokenUsagePreservesPromptUsage(t *testing.T) {
	state := &sessionState{}
	firstRead, firstWrite := 4_000, 1_000
	first := state.tokenUsageForPrompt("dsh", &acpsdk.Usage{
		InputTokens:       5_500,
		OutputTokens:      500,
		CachedReadTokens:  &firstRead,
		CachedWriteTokens: &firstWrite,
	})
	if first == nil || first.InputTokens != 5_500 || first.OutputTokens != 500 {
		t.Fatalf("first usage = %#v", first)
	}

	secondRead, secondWrite := 12_000, 1_500
	second := state.tokenUsageForPrompt("dsh", &acpsdk.Usage{
		InputTokens:       14_000,
		OutputTokens:      1_600,
		CachedReadTokens:  &secondRead,
		CachedWriteTokens: &secondWrite,
	})
	if second == nil || second.InputTokens != 14_000 || second.OutputTokens != 1_600 {
		t.Fatalf("second usage = %#v", second)
	}
	if second.CacheReadTokens == nil || *second.CacheReadTokens != 12_000 {
		t.Fatalf("second cache read = %#v", second.CacheReadTokens)
	}
	if second.CacheWriteTokens == nil || *second.CacheWriteTokens != 1_500 {
		t.Fatalf("second cache write = %#v", second.CacheWriteTokens)
	}
}

func TestACPTokenUsageConvertsCumulativeCountersToTurnDelta(t *testing.T) {
	for _, agentName := range []string{"copilot", "unknown", "deepseek", ""} {
		t.Run(agentName, func(t *testing.T) {
			state := &sessionState{}
			firstRead, firstWrite := 4_000, 1_000
			first := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{
				InputTokens:       5_500,
				OutputTokens:      500,
				CachedReadTokens:  &firstRead,
				CachedWriteTokens: &firstWrite,
			})
			if first == nil || first.InputTokens != 5_500 || first.OutputTokens != 500 {
				t.Fatalf("first usage = %#v", first)
			}

			secondRead, secondWrite := 12_000, 1_500
			second := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{
				InputTokens:       14_000,
				OutputTokens:      1_600,
				CachedReadTokens:  &secondRead,
				CachedWriteTokens: &secondWrite,
			})
			if second == nil || second.InputTokens != 8_500 || second.OutputTokens != 1_100 {
				t.Fatalf("second usage = %#v", second)
			}
			if second.CacheReadTokens == nil || *second.CacheReadTokens != 8_000 {
				t.Fatalf("second cache read = %#v", second.CacheReadTokens)
			}
			if second.CacheWriteTokens == nil || *second.CacheWriteTokens != 500 {
				t.Fatalf("second cache write = %#v", second.CacheWriteTokens)
			}
		})
	}
}

func TestACPTokenUsageCounterResetAndMissingUsage(t *testing.T) {
	for _, agentName := range []string{"dsh", "copilot"} {
		t.Run(agentName, func(t *testing.T) {
			state := &sessionState{}
			state.tokenUsageForPrompt(agentName, &acpsdk.Usage{InputTokens: 1000, OutputTokens: 100})
			if got := state.tokenUsageForPrompt(agentName, nil); got != nil {
				t.Fatalf("missing usage = %#v", got)
			}
			got := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{InputTokens: 500, OutputTokens: 50})
			if got == nil || got.InputTokens != 500 || got.OutputTokens != 50 {
				t.Fatalf("usage after counter reset = %#v", got)
			}
		})
	}
}

func TestACPUsageUpdateReplacesCurrentContextUsage(t *testing.T) {
	state := &sessionState{contextWindow: types.ContextWindow{
		TotalTokens:        1_911_735,
		ModelContextWindow: 512_000,
	}}

	state.setUsageUpdate(217_991, 512_000)
	got := state.getContextWindow()
	if got.TotalTokens != 217_991 || got.ModelContextWindow != 512_000 {
		t.Fatalf("context window after first update = %#v", got)
	}
	if !state.hasContextUsageUpdate() {
		t.Fatal("usage update was not marked authoritative")
	}

	state.setUsageUpdate(220_104, 512_000)
	got = state.getContextWindow()
	if got.TotalTokens != 220_104 || got.ModelContextWindow != 512_000 {
		t.Fatalf("context window after second update = %#v", got)
	}
}

func TestCloseProcessesConcurrentlyDoesNotSerializeWaits(t *testing.T) {
	procs := []*Process{{agentName: "first"}, {agentName: "second"}}
	started := make(chan string, len(procs))
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		closeProcessesConcurrently(procs, func(proc *Process) error {
			started <- proc.agentLabel()
			<-release
			return nil
		})
		close(done)
	}()

	seen := make(map[string]bool, len(procs))
	for range procs {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("process closes were serialized")
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("started closes = %v", seen)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent process closes did not complete")
	}
}

func TestWrapSessionUpdateRecognizesPlan(t *testing.T) {
	update := wrapSessionUpdate("session-1", acpsdk.SessionUpdate{
		Plan: &acpsdk.SessionUpdatePlan{
			Entries: []acpsdk.PlanEntry{{Content: "Inspect files", Status: acpsdk.PlanEntryStatusPending}},
		},
	})
	if update.Type != UpdateTypePlan {
		t.Fatalf("update.Type = %q, want %q", update.Type, UpdateTypePlan)
	}
}

func TestMapModelStateUsesLegacyACPModels(t *testing.T) {
	models := mapModelState(&acpsdk.SessionModelState{
		CurrentModelId: acpsdk.ModelId("gpt-4.1"),
		AvailableModels: []acpsdk.ModelInfo{
			{
				ModelId:     acpsdk.ModelId("gpt-4.1"),
				Name:        "GPT-4.1",
				Description: acpsdk.Ptr("Fast model"),
			},
		},
	})
	if models.CurrentModelID != "gpt-4.1" {
		t.Fatalf("CurrentModelID = %q", models.CurrentModelID)
	}
	if len(models.Models) != 1 {
		t.Fatalf("Models = %#v", models.Models)
	}
	if got := models.Models[0]; got.ID != "gpt-4.1" || got.Name != "GPT-4.1" || got.Description != "Fast model" {
		t.Fatalf("model = %#v", got)
	}
}

func TestConvertEventMapsACPPlanToTodoUpdate(t *testing.T) {
	event := convertEvent(SessionUpdate{
		Type:      UpdateTypePlan,
		SessionID: "session-1",
		Raw: acpsdk.SessionUpdate{
			Plan: &acpsdk.SessionUpdatePlan{
				Entries: []acpsdk.PlanEntry{
					{Content: "Inspect files", Status: acpsdk.PlanEntryStatusPending},
					{Content: "Patch implementation", Status: acpsdk.PlanEntryStatusInProgress},
					{Content: "Run tests", Status: acpsdk.PlanEntryStatusCompleted},
				},
			},
		},
	}, nil)
	if event.Type != types.EventTypeTodoUpdate {
		t.Fatalf("event.Type = %q, want %q", event.Type, types.EventTypeTodoUpdate)
	}
	todo, ok := event.Data.(types.TodoUpdate)
	if !ok {
		t.Fatalf("event.Data = %T, want TodoUpdate", event.Data)
	}
	if len(todo.Items) != 3 {
		t.Fatalf("todo.Items = %#v, want 3 items", todo.Items)
	}
	if todo.Items[0].Content != "Inspect files" || todo.Items[0].Status != "pending" {
		t.Fatalf("todo.Items[0] = %#v", todo.Items[0])
	}
	if todo.Items[1].Content != "Patch implementation" || todo.Items[1].Status != "in_progress" {
		t.Fatalf("todo.Items[1] = %#v", todo.Items[1])
	}
	if todo.Items[2].Content != "Run tests" || todo.Items[2].Status != "completed" {
		t.Fatalf("todo.Items[2] = %#v", todo.Items[2])
	}
}

func TestConvertEventPreservesACPHumanReadableToolTitle(t *testing.T) {
	event := convertEvent(SessionUpdate{
		Type:      UpdateTypeToolCall,
		SessionID: "session-1",
		Raw: acpsdk.SessionUpdate{
			ToolCall: &acpsdk.SessionUpdateToolCall{
				ToolCallId: "tool-1",
				Title:      "Run the test suite",
				Kind:       acpsdk.ToolKindExecute,
				Status:     acpsdk.ToolCallStatusInProgress,
			},
		},
	}, nil)
	toolCall, ok := event.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event.Data = %T, want ToolCall", event.Data)
	}
	if toolCall.Title != "Run the test suite" {
		t.Fatalf("title = %q, want ACP human-readable title", toolCall.Title)
	}
}

func TestConvertEventMapsACPPlanUpdateMarkdownAndFileToPlanUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  acpsdk.SessionUpdate
		want types.PlanUpdate
	}{
		{
			name: "markdown",
			raw: acpsdk.SessionUpdate{
				PlanUpdate: &acpsdk.SessionPlanUpdate{
					Plan: acpsdk.PlanUpdateContent{
						Markdown: &acpsdk.PlanUpdateContentMarkdown{
							Id:      "plan-1",
							Content: "## Plan\n\n- Step",
						},
					},
				},
			},
			want: types.PlanUpdate{ID: "plan-1", Content: "## Plan\n\n- Step"},
		},
		{
			name: "file",
			raw: acpsdk.SessionUpdate{
				PlanUpdate: &acpsdk.SessionPlanUpdate{
					Plan: acpsdk.PlanUpdateContent{
						File: &acpsdk.PlanUpdateContentFile{
							Id:  "plan-3",
							Uri: "file:///tmp/PLAN.md",
						},
					},
				},
			},
			want: types.PlanUpdate{ID: "plan-3", Content: "Plan file: file:///tmp/PLAN.md"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := convertEvent(SessionUpdate{
				Type:      UpdateTypePlan,
				SessionID: "session-1",
				Raw:       tc.raw,
			}, nil)
			if event.Type != types.EventTypePlanUpdate {
				t.Fatalf("event.Type = %q, want %q", event.Type, types.EventTypePlanUpdate)
			}
			got, ok := event.Data.(types.PlanUpdate)
			if !ok {
				t.Fatalf("event.Data = %T, want PlanUpdate", event.Data)
			}
			if got != tc.want {
				t.Fatalf("plan = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestConvertEventMapsACPPlanUpdateItemsToTodoUpdate(t *testing.T) {
	event := convertEvent(SessionUpdate{
		Type:      UpdateTypePlan,
		SessionID: "session-1",
		Raw: acpsdk.SessionUpdate{
			PlanUpdate: &acpsdk.SessionPlanUpdate{
				Plan: acpsdk.PlanUpdateContent{
					Items: &acpsdk.PlanUpdateContentItems{
						Id: "plan-2",
						Entries: []acpsdk.PlanEntry{
							{Content: "Verify behavior", Status: acpsdk.PlanEntryStatusCompleted},
						},
					},
				},
			},
		},
	}, nil)
	if event.Type != types.EventTypeTodoUpdate {
		t.Fatalf("event.Type = %q, want %q", event.Type, types.EventTypeTodoUpdate)
	}
	todo, ok := event.Data.(types.TodoUpdate)
	if !ok {
		t.Fatalf("event.Data = %T, want TodoUpdate", event.Data)
	}
	if len(todo.Items) != 1 || todo.Items[0].Content != "Verify behavior" || todo.Items[0].Status != "completed" {
		t.Fatalf("todo = %#v", todo)
	}
}

func TestConvertEventSuppressesACPTodoWriteToolCards(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  acpsdk.SessionUpdate
		typ  UpdateType
	}{
		{
			name: "pending todowrite",
			typ:  UpdateTypeToolCall,
			raw: acpsdk.SessionUpdate{
				ToolCall: &acpsdk.SessionUpdateToolCall{
					ToolCallId: "call-1",
					Title:      "todowrite",
					Kind:       acpsdk.ToolKindOther,
					Status:     acpsdk.ToolCallStatusPending,
					RawInput:   map[string]any{},
				},
			},
		},
		{
			name: "complete todowrite",
			typ:  UpdateTypeToolUpdate,
			raw: acpsdk.SessionUpdate{
				ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
					ToolCallId: "call-1",
					Title:      acpsdk.Ptr("todowrite"),
					Kind:       acpsdk.Ptr(acpsdk.ToolKindOther),
					Status:     acpsdk.Ptr(acpsdk.ToolCallStatusCompleted),
					RawInput:   map[string]any{"todos": []any{map[string]any{"content": "Inspect", "status": "pending"}}},
				},
			},
		},
		{
			name: "summary todos",
			typ:  UpdateTypeToolUpdate,
			raw: acpsdk.SessionUpdate{
				ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
					ToolCallId: "call-1",
					Title:      acpsdk.Ptr("5 todos"),
					Kind:       acpsdk.Ptr(acpsdk.ToolKindOther),
					Status:     acpsdk.Ptr(acpsdk.ToolCallStatusCompleted),
					RawOutput:  map[string]any{"metadata": map[string]any{"todos": []any{map[string]any{"content": "Inspect", "status": "pending"}}}},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := convertEvent(SessionUpdate{
				Type:      tc.typ,
				SessionID: "session-1",
				Raw:       tc.raw,
			}, nil)
			if event.Type != "" {
				t.Fatalf("event = %#v, want suppressed empty event", event)
			}
		})
	}
}

// acpAskUserRawInput 构造 dsh ask_user_question 的工具参数，形状与 openma 适配器
// classifyToolCall 产出的 rawInput 一致（snake_case 的 multi_select）。
func acpAskUserRawInput(questions ...map[string]any) map[string]any {
	// JSON 解码出来的数组是 []any，不是 []map[string]any —— 与线上形状保持一致。
	list := make([]any, 0, len(questions))
	for _, question := range questions {
		list = append(list, question)
	}
	return map[string]any{"questions": list}
}

func TestConvertEventRetagsACPAskUserToolCard(t *testing.T) {
	raw := acpAskUserRawInput(map[string]any{
		"id":           "confirm",
		"question":     "继续吗？",
		"header":       "确认",
		"multi_select": false,
		"options": []any{
			map[string]any{"label": "继续", "description": "按当前方案执行"},
			map[string]any{"label": "停止"},
		},
	})

	var gotCallID string
	var gotQuestions []elicitationQuestion
	event := convertEvent(SessionUpdate{
		Type:      UpdateTypeToolCall,
		SessionID: "session-1",
		Raw: acpsdk.SessionUpdate{
			ToolCall: &acpsdk.SessionUpdateToolCall{
				ToolCallId: "call-ask-1",
				Title:      "ask_user_question",
				Kind:       acpsdk.ToolKindOther,
				Status:     acpsdk.ToolCallStatusInProgress,
				RawInput:   raw,
			},
		},
	}, func(callID string, questions []elicitationQuestion) {
		gotCallID = callID
		gotQuestions = questions
	})

	toolCall, ok := event.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event.Data = %T, want ToolCall", event.Data)
	}
	if toolCall.Kind != types.ToolKindAskUser {
		t.Fatalf("kind = %q, want %q", toolCall.Kind, types.ToolKindAskUser)
	}
	if toolCall.CallID != "call-ask-1" {
		t.Fatalf("callID = %q, want %q", toolCall.CallID, "call-ask-1")
	}
	items, ok := toolCall.Meta["questions"].([]types.AskUserQuestionItem)
	if !ok || len(items) != 1 {
		t.Fatalf("meta.questions = %#v, want one item", toolCall.Meta["questions"])
	}
	if items[0].Question != "继续吗？" || items[0].Header != "确认" || !items[0].MultiSelect == false {
		t.Fatalf("question = %#v", items[0])
	}
	if len(items[0].Options) != 2 || items[0].Options[0].Label != "继续" || items[0].Options[1].Label != "停止" {
		t.Fatalf("options = %#v", items[0].Options)
	}
	if gotCallID != "call-ask-1" || len(gotQuestions) != 1 {
		t.Fatalf("onAskUser = (%q, %#v), want (call-ask-1, one question)", gotCallID, gotQuestions)
	}
	if gotQuestions[0].ID != "confirm" || gotQuestions[0].MultiSelect {
		t.Fatalf("registered question = %#v", gotQuestions[0])
	}
	if len(gotQuestions[0].Options) != 2 || gotQuestions[0].Options[0] != "继续" {
		t.Fatalf("registered options = %#v", gotQuestions[0].Options)
	}
}

func TestConvertEventKeepsACPAskUserKindOnToolUpdate(t *testing.T) {
	raw := acpAskUserRawInput(map[string]any{
		"id":       "mode",
		"question": "选哪种模式？",
		"options":  []any{map[string]any{"label": "快速"}, map[string]any{"label": "完整"}},
	})

	var askUserCalls int
	event := convertEvent(SessionUpdate{
		Type:      UpdateTypeToolUpdate,
		SessionID: "session-1",
		Raw: acpsdk.SessionUpdate{
			ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
				ToolCallId: "call-ask-1",
				Title:      acpsdk.Ptr("ask_user_question"),
				Kind:       acpsdk.Ptr(acpsdk.ToolKindOther),
				Status:     acpsdk.Ptr(acpsdk.ToolCallStatusCompleted),
				RawInput:   raw,
			},
		},
	}, func(string, []elicitationQuestion) { askUserCalls++ })

	toolCall, ok := event.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event.Data = %T, want ToolCall", event.Data)
	}
	if toolCall.Kind != types.ToolKindAskUser {
		t.Fatalf("kind = %q, want %q（update 不能把 ask_user 覆盖回 other）", toolCall.Kind, types.ToolKindAskUser)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if askUserCalls != 1 {
		t.Fatalf("onAskUser calls = %d, want 1", askUserCalls)
	}
}

func TestConvertEventIgnoresNonAskUserToolCards(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]any
	}{
		{name: "no questions", raw: map[string]any{"command": "ls"}},
		{name: "empty questions", raw: map[string]any{"questions": []any{}}},
		{name: "question without text", raw: map[string]any{"questions": []any{map[string]any{"id": "x"}}}},
		{name: "questions not a list", raw: map[string]any{"questions": "nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := convertEvent(SessionUpdate{
				Type:      UpdateTypeToolCall,
				SessionID: "session-1",
				Raw: acpsdk.SessionUpdate{
					ToolCall: &acpsdk.SessionUpdateToolCall{
						ToolCallId: "call-1",
						Title:      "bash",
						Kind:       acpsdk.ToolKindExecute,
						Status:     acpsdk.ToolCallStatusInProgress,
						RawInput:   tc.raw,
					},
				},
			}, nil)
			toolCall, ok := event.Data.(types.ToolCall)
			if !ok {
				t.Fatalf("event.Data = %T, want ToolCall", event.Data)
			}
			if toolCall.Kind == types.ToolKindAskUser {
				t.Fatalf("kind = ask_user, want untouched for %#v", tc.raw)
			}
			if toolCall.Meta != nil {
				t.Fatalf("meta = %#v, want nil", toolCall.Meta)
			}
		})
	}
}
