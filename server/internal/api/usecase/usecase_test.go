package usecase

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func usecaseSessionKeys(items []*session.Session) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil {
			keys = append(keys, item.Key)
		}
	}
	return keys
}

func keysOfSessions(items []*session.Session) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil {
			keys = append(keys, item.Key)
		}
	}
	return keys
}

func TestDedupeExchangeAuxBufferMergesDuplicateToolCalls(t *testing.T) {
	items := []session.ExchangeAux{
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID:  "call-1",
				Title:   "Print hi",
				Status:  "running",
				Kind:    agenttypes.ToolKindTask,
				RawType: "tool_use",
				Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "prompt body"}},
				Meta:    map[string]any{"prompt": "prompt body"},
			},
		},
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID:  "call-1",
				Status:  "running",
				Kind:    agenttypes.ToolKindTask,
				RawType: "claude_task",
				Meta:    map[string]any{"lastToolName": "Bash"},
			},
		},
	}

	got := dedupeExchangeAuxBuffer(items)
	if len(got) != 1 || got[0].ToolCall == nil {
		t.Fatalf("deduped items = %#v, want one toolcall", got)
	}
	toolCall := got[0].ToolCall
	if toolCall.Title != "Print hi" || toolCall.RawType != "claude_task" {
		t.Fatalf("toolCall latest fields = %#v", toolCall)
	}
	if len(toolCall.Content) != 1 || toolCall.Content[0].Text != "prompt body" {
		t.Fatalf("toolCall content = %#v, want original prompt body", toolCall.Content)
	}
	if toolCall.Meta["prompt"] != "prompt body" || toolCall.Meta["lastToolName"] != "Bash" {
		t.Fatalf("toolCall meta = %#v", toolCall.Meta)
	}
}

func TestDedupeExchangeAuxBufferMergesTaskCreateAndUpdateByCallID(t *testing.T) {
	items := []session.ExchangeAux{
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "claude-task-list:7",
				Title:  "检查 git 状态",
				Status: "running",
				Kind:   agenttypes.ToolKindTask,
				Meta: map[string]any{
					"toolUseId": "call-create-1",
					"taskId":    "7",
					"taskTool":  "TaskCreate",
				},
			},
		},
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "claude-task-list:7",
				Status: "complete",
				Kind:   agenttypes.ToolKindTask,
				Meta: map[string]any{
					"toolUseId":   "call-update-1",
					"taskId":      "7",
					"taskStatus":  "complete",
					"taskTool":    "TaskUpdate",
					"updatedOnly": true,
				},
			},
		},
	}

	got := dedupeExchangeAuxBuffer(items)
	if len(got) != 1 || got[0].ToolCall == nil {
		t.Fatalf("deduped items = %#v, want one toolcall", got)
	}
	if got[0].ToolCall.CallID != "claude-task-list:7" {
		t.Fatalf("callID = %q, want real task call id", got[0].ToolCall.CallID)
	}
	if got[0].ToolCall.Title != "检查 git 状态" {
		t.Fatalf("title = %q, want original create title", got[0].ToolCall.Title)
	}
	if got[0].ToolCall.Status != "complete" {
		t.Fatalf("status = %q, want latest task status", got[0].ToolCall.Status)
	}
	if got[0].ToolCall.Meta["taskId"] != "7" || got[0].ToolCall.Meta["taskTool"] != "TaskCreate" || got[0].ToolCall.Meta["updatedOnly"] != true {
		t.Fatalf("meta = %#v, want merged task metadata", got[0].ToolCall.Meta)
	}
}

func TestDedupeExchangeAuxBufferKeepsLatestTodoState(t *testing.T) {
	items := []session.ExchangeAux{
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "todo-call-1",
				Title:  "todos",
				Status: "running",
				Kind:   agenttypes.ToolKindTodo,
				Content: []agenttypes.ToolCallContentItem{{
					Type: "text",
					Text: "- [ ] first",
				}},
			},
		},
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "todo-call-2",
				Title:  "todos",
				Status: "running",
				Kind:   agenttypes.ToolKindTodo,
				Content: []agenttypes.ToolCallContentItem{{
					Type: "text",
					Text: "- [x] first\n- [ ] second",
				}},
			},
		},
		{
			Seq:  1,
			Line: 0,
			Todo: &agenttypes.TodoUpdate{
				Items: []agenttypes.TodoItem{{Content: "codex final", Status: "completed"}},
			},
		},
	}

	got := dedupeExchangeAuxBuffer(items)
	if len(got) != 1 || got[0].Todo == nil {
		t.Fatalf("deduped items = %#v, want latest codex todo", got)
	}
	if got[0].Todo.Items[0].Content != "codex final" {
		t.Fatalf("todo = %#v, want latest state", got[0].Todo)
	}
}

func TestDedupeExchangeAuxBufferMergesDuplicateTodoToolCallBeforeKeepingLatest(t *testing.T) {
	items := []session.ExchangeAux{
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "todo-call-1",
				Title:  "todos",
				Status: "running",
				Kind:   agenttypes.ToolKindTodo,
				Content: []agenttypes.ToolCallContentItem{{
					Type: "text",
					Text: "- [ ] first",
				}},
			},
		},
		{
			Seq:  1,
			Line: 0,
			ToolCall: &agenttypes.ToolCall{
				CallID: "todo-call-1",
				Status: "complete",
				Kind:   agenttypes.ToolKindTodo,
			},
		},
	}

	got := dedupeExchangeAuxBuffer(items)
	if len(got) != 1 || got[0].ToolCall == nil {
		t.Fatalf("deduped items = %#v, want one todo toolcall", got)
	}
	if got[0].ToolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", got[0].ToolCall.Status)
	}
	if len(got[0].ToolCall.Content) != 1 || got[0].ToolCall.Content[0].Text != "- [ ] first" {
		t.Fatalf("content = %#v, want merged original content", got[0].ToolCall.Content)
	}
}

func TestShouldPersistToolCallAuxSkipsClaudeTaskProgress(t *testing.T) {
	if shouldPersistToolCallAux(agenttypes.ToolCall{
		CallID:  "call-1",
		Kind:    agenttypes.ToolKindTask,
		RawType: "claude_task",
		Meta:    map[string]any{"subtype": "task_progress"},
	}) {
		t.Fatalf("claude task progress should not persist")
	}
	if !shouldPersistToolCallAux(agenttypes.ToolCall{
		CallID:  "call-1",
		Kind:    agenttypes.ToolKindTask,
		RawType: "claude_task",
		Meta:    map[string]any{"subtype": "task_started"},
	}) {
		t.Fatalf("claude task start should persist")
	}
}

func sendCommandAndFinal(t *testing.T, service Service, rootID, sessionKey, command string) (string, error) {
	t.Helper()
	var final string
	err := service.SendMessage(context.Background(), SendMessageInput{
		RootID:  rootID,
		Key:     sessionKey,
		Content: command,
		OnUpdate: func(event agenttypes.Event) {
			toolCall, ok := event.Data.(agenttypes.ToolCall)
			if !ok || toolCall.Meta["source"] != "userShell" || toolCall.Meta["phase"] != "final" {
				return
			}
			if len(toolCall.Content) > 0 {
				final = toolCall.Content[0].Text
			}
		},
	})
	return final, err
}

func selectRunnableAgent(cfg agent.Config) (string, bool) {
	want := strings.TrimSpace(os.Getenv("MINDFS_IT_AGENT_NAME"))
	if want != "" {
		def, ok := cfg.GetAgent(want)
		if !ok {
			return "", false
		}
		if _, err := exec.LookPath(def.Command); err != nil {
			return "", false
		}
		return want, true
	}

	for _, name := range []string{"codex", "claude", "gemini"} {
		def, ok := cfg.GetAgent(name)
		if !ok {
			continue
		}
		if _, err := exec.LookPath(def.Command); err != nil {
			continue
		}
		return name, true
	}
	return "", false
}
