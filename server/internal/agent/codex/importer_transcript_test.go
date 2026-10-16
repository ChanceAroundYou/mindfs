package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestInspectCodexSessionFileSkipsInjectedUserPromptBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"session_meta","payload":{"id":"session-1","cwd":"/project"}}
{"timestamp":"2026-07-28T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>internal</recommended_plugins>"},{"type":"input_text","text":"# AGENTS.md instructions\n<INSTRUCTIONS>internal</INSTRUCTIONS>"},{"type":"input_text","text":"<environment_context>internal</environment_context>"}]}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"actual request"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	item, ok, err := inspectCodexSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("inspectCodexSessionFile returned not found")
	}
	if item.FirstUserText != "actual request" {
		t.Fatalf("FirstUserText = %q, want actual request", item.FirstUserText)
	}
}

func TestExtractCodexImportedUserTextDropsOnlyInjectedBlocks(t *testing.T) {
	raw := []any{
		map[string]any{"type": "input_text", "text": "<recommended_plugins>internal</recommended_plugins>"},
		map[string]any{"type": "input_text", "text": "# AGENTS.md instructions\n<INSTRUCTIONS>internal</INSTRUCTIONS>"},
		map[string]any{"type": "input_text", "text": "first actual block"},
		map[string]any{"type": "input_text", "text": "<environment_context>internal</environment_context>"},
		map[string]any{"type": "input_text", "text": "second actual block"},
	}
	want := "first actual block\n\nsecond actual block"
	if got := extractCodexImportedUserText(raw); got != want {
		t.Fatalf("imported text = %q, want %q", got, want)
	}
}

func TestExtractCodexImportedUserTextPreservesMixedBlock(t *testing.T) {
	raw := []any{map[string]any{
		"type": "input_text",
		"text": "<environment_context>internal</environment_context>\nactual request",
	}}
	want := "<environment_context>internal</environment_context>\nactual request"
	if got := extractCodexImportedUserText(raw); got != want {
		t.Fatalf("imported text = %q, want %q", got, want)
	}
}

func TestReadCodexImportedExchangeLocatorsAppliesRollbackBeforeTimestampFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	content := `{"timestamp":"2026-06-22T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u1"}]}}
{"timestamp":"2026-06-22T01:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}}
{"timestamp":"2026-06-22T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u2"}]}}
{"timestamp":"2026-06-22T01:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a2"}]}}
{"timestamp":"2026-06-22T01:00:04Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}
{"timestamp":"2026-06-22T01:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u3"}]}}
{"timestamp":"2026-06-22T01:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a3"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := readCodexImportedExchangeLocators(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("len(all) = %d, want 4", len(all))
	}
	if all[0].Content != "u1" || all[1].Content != "a1" || all[2].Content != "u3" || all[3].Content != "a3" {
		t.Fatalf("effective exchanges = %#v", all)
	}
	if all[3].CodexUserCountAfter != 2 {
		t.Fatalf("a3 user count = %d, want 2", all[3].CodexUserCountAfter)
	}

	after, err := time.Parse(time.RFC3339, "2026-06-22T01:00:01Z")
	if err != nil {
		t.Fatal(err)
	}
	delta, err := readCodexImportedExchangeLocators(path, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 2 {
		t.Fatalf("len(delta) = %d, want 2", len(delta))
	}
	if delta[0].Content != "u3" || delta[1].Content != "a3" {
		t.Fatalf("delta exchanges = %#v", delta)
	}
}

func TestReadCodexImportedExchangesIncludesFunctionAndCustomToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}}
{"timestamp":"2026-07-28T01:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking"}]}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}","call_id":"call-1"}}
{"timestamp":"2026-07-28T01:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"{\"output\":\"ok\",\"exit_code\":0}"}}
{"timestamp":"2026-07-28T01:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: server/main.go\n@@\n-old\n+new\n*** Add File: docs/note.md\n+# Note\n*** End Patch","call_id":"call-2","status":"completed"}}
{"timestamp":"2026-07-28T01:00:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-2","output":"Done!"}}
{"timestamp":"2026-07-28T01:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixed"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readCodexImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %#v", len(items), items)
	}
	assistant := items[1]
	if assistant.Content != "checking\n\nfixed" {
		t.Fatalf("assistant content = %q", assistant.Content)
	}
	if len(assistant.Aux) != 2 {
		t.Fatalf("len(aux) = %d, want 2: %#v", len(assistant.Aux), assistant.Aux)
	}

	command := assistant.Aux[0].ToolCall
	if command == nil || command.CallID != "call-1" ||
		command.Kind != agenttypes.ToolKindExecute || command.Status != "complete" {
		t.Fatalf("command tool call = %#v", command)
	}
	if command.Title != "go test ./..." || command.Meta["command"] != "go test ./..." {
		t.Fatalf("command summary = title %q meta %#v", command.Title, command.Meta)
	}
	if output, _ := command.Meta["output"].(string); output != "ok" {
		t.Fatalf("command output = %#v, want ok", command.Meta["output"])
	}
	if assistant.Aux[0].Line != 1 {
		t.Fatalf("command line = %d, want 1", assistant.Aux[0].Line)
	}

	patch := assistant.Aux[1].ToolCall
	if patch == nil || patch.CallID != "call-2" ||
		patch.Kind != agenttypes.ToolKindEdit || patch.Status != "complete" {
		t.Fatalf("patch tool call = %#v", patch)
	}
	if len(patch.Locations) != 2 ||
		patch.Locations[0].Path != "server/main.go" ||
		patch.Locations[1].Path != "docs/note.md" {
		t.Fatalf("patch locations = %#v", patch.Locations)
	}
	if len(patch.Content) != 2 ||
		patch.Content[0].Path != "server/main.go" ||
		patch.Content[0].ChangeKind != "update" ||
		patch.Content[0].Text != "--- a/server/main.go\n+++ b/server/main.go\n@@\n-old\n+new" ||
		patch.Content[1].Path != "docs/note.md" ||
		patch.Content[1].ChangeKind != "add" ||
		patch.Content[1].Text != "# Note" {
		t.Fatalf("patch content = %#v", patch.Content)
	}
	if output, _ := patch.Meta["output"].(string); output != "Done!" {
		t.Fatalf("patch output = %#v, want Done!", patch.Meta["output"])
	}
}

func TestReadCodexImportedExchangesConvertsProposedPlanToPlanAux(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Here is the plan.\n\n<proposed_plan>\n# Implementation\n\n- Inspect\n- Patch\n</proposed_plan>\n\nLet me know."}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := readCodexImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1: %#v", len(items), items)
	}
	if items[0].Content != "Here is the plan.\n\nLet me know." {
		t.Fatalf("content = %q", items[0].Content)
	}
	if len(items[0].Aux) != 1 || items[0].Aux[0].Plan == nil {
		t.Fatalf("aux = %#v, want plan", items[0].Aux)
	}
	if items[0].Aux[0].Plan.Content != "# Implementation\n\n- Inspect\n- Patch" {
		t.Fatalf("plan = %#v", items[0].Aux[0].Plan)
	}
	if items[0].Aux[0].Line != 1 {
		t.Fatalf("plan line = %d, want 1", items[0].Aux[0].Line)
	}
}

func TestReadCodexImportedExchangesUsesRecordedWorldStateShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"test it"}]}}
{"timestamp":"2026-07-28T01:00:01Z","type":"world_state","payload":{"full":true,"state":{"environments":{"environments":{"local":{"cwd":"/tmp/project","status":"available","shell":"zsh"}}}}}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"const r = await tools.exec_command({\"cmd\":\"go test ./...\",\"workdir\":\"/tmp/project\"});\ntext(r.output);","call_id":"call-exec","status":"completed"}}
{"timestamp":"2026-07-28T01:00:03Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-exec","output":[{"type":"input_text","text":"Script completed\nWall time 0.8 seconds\nOutput:\n"},{"type":"input_text","text":"ok\n"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readCodexImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || len(items[1].Aux) != 1 || items[1].Aux[0].ToolCall == nil {
		t.Fatalf("imported exchanges = %#v", items)
	}
	toolCall := items[1].Aux[0].ToolCall
	if toolCall.Title != `zsh -lc 'go test ./...'` ||
		toolCall.Meta["command"] != `zsh -lc 'go test ./...'` ||
		toolCall.Meta["output"] != "ok" {
		t.Fatalf("imported exec tool call = %#v", toolCall)
	}
}
