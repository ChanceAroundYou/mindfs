package usecase

import (
	"context"
	"os"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func TestProvisionalStreamWriterPersistsTextAndToolCall(t *testing.T) {
	_, manager := newLiveTailTestManager(t)
	created, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Stream"})
	if err != nil {
		t.Fatal(err)
	}
	const turnID = "22222222-3333-4444-5555-666666666666"
	w := newProvisionalStreamWriter(manager, created.Key, 1, turnID)
	if w == nil {
		t.Fatal("writer should be created")
	}
	w.text("我先说明一下。")
	w.toolCall(agenttypes.ToolCall{CallID: "c1", Title: "Bash", Status: "running", Kind: agenttypes.ToolKindExecute})
	w.toolCall(agenttypes.ToolCall{CallID: "c1", Title: "Bash", Status: "running", Kind: agenttypes.ToolKindExecute})
	w.text("工具还没返回。")

	blocks, err := manager.ReadProvisionalTurnBlocks(context.Background(), created.Key, 2, turnID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("provisional blocks = %d, want 3 (重复 tool call 应去重)", len(blocks))
	}
	if blocks[0].Text != "我先说明一下。" || blocks[2].Text != "工具还没返回。" {
		t.Fatalf("text order/content wrong: %#v", blocks)
	}
	if blocks[1].ToolCall == nil || blocks[1].ToolCall.CallID != "c1" {
		t.Fatalf("tool call block missing: %#v", blocks[1])
	}
}

func TestSendPathRoutesStreamEventsIntoProvisionalWriter(t *testing.T) {
	body, err := os.ReadFile("session_send.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	for _, needle := range []string{
		"provisional := newProvisionalStreamWriter(",
		"provisional.text(chunk.Content)",
		"provisional.toolCall(toolCall)",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("send path lost provisional wiring: %q", needle)
		}
	}
}
