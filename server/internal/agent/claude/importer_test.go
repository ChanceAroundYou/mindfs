package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractClaudeUserPreviewUsesLastMeaningfulContentBlock(t *testing.T) {
	raw := []any{
		map[string]any{"type": "text", "text": "<local-command-caveat>internal</local-command-caveat>"},
		map[string]any{"type": "text", "text": "actual request"},
	}
	if got := extractClaudeUserPreview(raw); got != "actual request" {
		t.Fatalf("preview = %q, want actual request", got)
	}
}

func TestExtractClaudeImportedUserTextDropsOnlyInjectedBlocks(t *testing.T) {
	raw := []any{
		map[string]any{"type": "text", "text": "<local-command-caveat>internal</local-command-caveat>"},
		map[string]any{"type": "text", "text": "first actual block"},
		map[string]any{"type": "text", "text": "second actual block"},
	}
	want := "first actual block\n\nsecond actual block"
	if got := extractClaudeImportedUserText(raw); got != want {
		t.Fatalf("imported text = %q, want %q", got, want)
	}
}

func TestReadClaudeImportedSubagentsLinksAgentToolCall(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.jsonl")
	parent := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-agent-1","name":"Agent","input":{"description":"Inspect importer","subagent_type":"Explore","model":"sonnet"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-agent-1","content":"done"}]},"toolUseResult":{"agentId":"child-1","agentType":"Explore","resolvedModel":"claude-sonnet"}}
`
	if err := os.WriteFile(parentPath, []byte(parent), 0o600); err != nil {
		t.Fatal(err)
	}
	subagentDir := filepath.Join(dir, "parent", "subagents")
	if err := os.MkdirAll(subagentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	child := `{"type":"user","agentId":"child-1","timestamp":"2026-07-28T01:00:00Z","message":{"content":"inspect"}}
{"type":"assistant","agentId":"child-1","timestamp":"2026-07-28T01:00:01Z","message":{"content":[{"type":"text","text":"found it"}]}}
`
	if err := os.WriteFile(filepath.Join(subagentDir, "agent-child-1.jsonl"), []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := NewImporter(ImporterOptions{}).readClaudeImportedSubagents(parentPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(subagents) = %d, want 1: %#v", len(items), items)
	}
	item := items[0]
	if item.AgentSessionID != "claude-subagent:child-1" ||
		item.ParentToolCallID != "tool-agent-1" ||
		item.Title != "Inspect importer" ||
		item.Model != "sonnet" {
		t.Fatalf("subagent = %#v", item)
	}
	if len(item.Exchanges) != 2 {
		t.Fatalf("len(exchanges) = %d, want 2", len(item.Exchanges))
	}
}
