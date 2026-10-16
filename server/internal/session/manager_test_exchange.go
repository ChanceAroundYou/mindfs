package session

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestManagerPersistsExchangeModelDisplayName(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:  TypeChat,
		Agent: "claude",
		Model: "opus",
		Name:  "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := WithExchangeModelDisplayName(context.Background(), "glm-4.7")
	if err := manager.AddExchangeForAgent(ctx, created, "agent", "reply", "claude", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(loaded.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(loaded.Exchanges))
	}
	if got := loaded.Exchanges[0].Model; got != "opus" {
		t.Fatalf("exchange model = %q, want runtime id", got)
	}
	if got := loaded.Exchanges[0].ModelDisplayName; got != "glm-4.7" {
		t.Fatalf("exchange model display name = %q, want snapshot", got)
	}
}

func TestManagerPersistsExchangeTokenUsage(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat, Agent: "codex", Model: "gpt-test", Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cacheRead := 8_000
	usage := &agenttypes.TokenUsage{
		InputTokens: 10_000, OutputTokens: 1_200, CacheReadTokens: &cacheRead,
	}
	ctx := WithExchangeTokenUsage(context.Background(), usage)
	if err := manager.AddExchangeForAgent(ctx, created, "agent", "reply", "codex", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	got := loaded.Exchanges[0].TokenUsage
	if got == nil || got.InputTokens != 10_000 || got.OutputTokens != 1_200 {
		t.Fatalf("token usage = %#v", got)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != 8_000 {
		t.Fatalf("cache read = %#v", got.CacheReadTokens)
	}
}

func TestManagerPersistsLastContextWindow(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:  TypeChat,
		Agent: "codex",
		Model: "gpt-test",
		Name:  "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	want := agenttypes.ContextWindow{
		TotalTokens:        12345,
		ModelContextWindow: 200000,
	}
	if err := manager.UpdateLastContextWindow(context.Background(), created, want); err != nil {
		t.Fatalf("update context window: %v", err)
	}
	manager.sessions = map[string]*Session{}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loaded.LastContextWindow != want {
		t.Fatalf("LastContextWindow = %#v, want %#v", loaded.LastContextWindow, want)
	}
}

func TestManagerStoresFullToolCallAndReturnsCompactedAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	content := "full search output"
	err = manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  "call-1",
			Title:   "search",
			Status:  "complete",
			Kind:    agenttypes.ToolKindSearch,
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: content}},
			Meta:    map[string]any{"output": content, "query": "full"},
		},
	})
	if err != nil {
		t.Fatalf("add aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 1 || aux[2][0].ToolCall == nil {
		t.Fatalf("aux[2] = %#v, want compacted toolcall", aux[2])
	}
	if len(aux[2][0].ToolCall.Content) != 0 {
		t.Fatalf("compacted content = %#v, want empty", aux[2][0].ToolCall.Content)
	}
	if output, ok := aux[2][0].ToolCall.Meta["output"]; ok {
		t.Fatalf("compacted meta output = %#v, want omitted", output)
	}
	if aux[2][0].ToolCall.Meta["query"] != "full" {
		t.Fatalf("compacted meta = %#v, want non-output keys preserved", aux[2][0].ToolCall.Meta)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, "call-1")
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if len(toolCall.Content) != 1 || !strings.Contains(toolCall.Content[0].Text, content) {
		t.Fatalf("full content = %#v, want %q", toolCall.Content, content)
	}
	if toolCall.Meta["output"] != content {
		t.Fatalf("full meta output = %#v, want %q", toolCall.Meta["output"], content)
	}
}

func TestManagerStoresPlanAndCompactAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Plan: &agenttypes.PlanUpdate{
			ID:      "plan-1",
			Content: "- inspect\n- patch",
		},
	}); err != nil {
		t.Fatalf("add plan aux: %v", err)
	}
	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Compact: &agenttypes.CompactNotice{
			ID:     "compact-1",
			Status: "complete",
		},
	}); err != nil {
		t.Fatalf("add compact aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 2 {
		t.Fatalf("aux[2] length = %d, want 2: %#v", len(aux[2]), aux[2])
	}
	if aux[2][0].Plan == nil || aux[2][0].Plan.Content != "- inspect\n- patch" {
		t.Fatalf("plan aux = %#v", aux[2][0])
	}
	if aux[2][1].Compact == nil || aux[2][1].Compact.Status != "complete" {
		t.Fatalf("compact aux = %#v", aux[2][1])
	}
}

func TestManagerStoresTodoAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Todo: &agenttypes.TodoUpdate{
			Items: []agenttypes.TodoItem{{Content: "persist todos", Status: "in_progress"}},
		},
	}); err != nil {
		t.Fatalf("add todo aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 1 || aux[2][0].Todo == nil {
		t.Fatalf("aux[2] = %#v, want todo aux", aux[2])
	}
	if got := aux[2][0].Todo.Items[0].Content; got != "persist todos" {
		t.Fatalf("todo content = %q, want persist todos", got)
	}
}
