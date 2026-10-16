package codex

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestParseImportedCodexToolCallStructuresEditArguments(t *testing.T) {
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":      "function_call",
		"name":      "edit",
		"call_id":   "call-edit",
		"arguments": `{"file_path":"server/main.go","old_string":"old","new_string":"new"}`,
	}, 0)
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	if toolCall.Title != "server/main.go" ||
		len(toolCall.Locations) != 1 ||
		toolCall.Locations[0].Path != "server/main.go" {
		t.Fatalf("tool call summary = %#v", toolCall)
	}
	if len(toolCall.Content) != 1 ||
		toolCall.Content[0].Type != "diff" ||
		toolCall.Content[0].Path != "server/main.go" ||
		toolCall.Content[0].OldText == nil ||
		*toolCall.Content[0].OldText != "old" ||
		toolCall.Content[0].NewText != "new" {
		t.Fatalf("tool call content = %#v", toolCall.Content)
	}
}

func TestParseImportedCodexToolCallRejectsUnsupportedKinds(t *testing.T) {
	for _, name := range []string{"read_file", "web_search", "spawn_agent"} {
		if toolCall, ok := parseImportedCodexToolCall(map[string]any{
			"type":      "function_call",
			"name":      name,
			"call_id":   "unsupported",
			"arguments": `{}`,
		}, 0); ok {
			t.Fatalf("parseImportedCodexToolCall(%q) = %#v, want rejected", name, toolCall)
		}
	}
}

func TestParseImportedCodexToolCallIncludesAskUser(t *testing.T) {
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":      "function_call",
		"name":      "request_user_input",
		"call_id":   "ask-1",
		"arguments": `{"questions":[{"question":"Continue?","options":[{"label":"Yes"}]}]}`,
	}, 0)
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	if toolCall.Kind != agenttypes.ToolKindAskUser || toolCall.Meta["questions"] == nil {
		t.Fatalf("ask-user tool call = %#v", toolCall)
	}
}

func TestImportedCodexAskUserAnswersUsesQuestionOrder(t *testing.T) {
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":    "function_call",
		"name":    "request_user_input",
		"call_id": "ask-1",
		"arguments": `{"questions":[
			{"id":"scope","question":"Scope?"},
			{"id":"locales","question":"Locales?","multi_select":true}
		]}`,
	}, 0)
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	answers := importedCodexAskUserAnswers(toolCall, `{
		"answers":{
			"locales":{"answers":["English","Chinese"]},
			"scope":{"answers":["All files"]}
		}
	}`)
	if answers["q_0"] != "All files" || answers["q_1"] != "English, Chinese" {
		t.Fatalf("answers = %#v", answers)
	}
}

func TestParseImportedCodexToolCallIncludesPlan(t *testing.T) {
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":      "function_call",
		"name":      "update_plan",
		"call_id":   "plan-1",
		"arguments": `{"plan":[{"step":"Inspect","status":"in_progress"}]}`,
	}, 0)
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	if toolCall.Kind != agenttypes.ToolKindThink || toolCall.Title != "update_plan" {
		t.Fatalf("plan tool call = %#v", toolCall)
	}
	if toolCall.Meta["input"] == nil {
		t.Fatalf("plan meta = %#v, want input", toolCall.Meta)
	}
}

func TestParseImportedCodexToolCallUnwrapsExecOrchestration(t *testing.T) {
	input := `const r = await tools.exec_command({"cmd":"go test ./... && git diff --check","workdir":"/tmp/project","yield_time_ms":30000});
text(r.output);`
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":    "custom_tool_call",
		"name":    "exec",
		"call_id": "call-exec",
		"input":   input,
	}, 0, "zsh")
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	wantCommand := `zsh -lc 'go test ./... && git diff --check'`
	if toolCall.Kind != agenttypes.ToolKindExecute ||
		toolCall.Title != wantCommand ||
		toolCall.Meta["command"] != wantCommand {
		t.Fatalf("exec tool call = %#v", toolCall)
	}
}

func TestParseImportedCodexToolCallUnwrapsJavaScriptObjectExec(t *testing.T) {
	input := `const r = await tools.exec_command({cmd:"node -e 'const value={ok:true}; console.log(value)'",workdir:"/tmp/project",yield_time_ms:30000}); text(r.output);`
	toolCall, ok := parseImportedCodexToolCall(map[string]any{
		"type":    "custom_tool_call",
		"name":    "exec",
		"call_id": "call-exec",
		"input":   input,
	}, 0, "zsh")
	if !ok {
		t.Fatal("parseImportedCodexToolCall returned false")
	}
	wantCommand := `zsh -lc 'node -e '\''const value={ok:true}; console.log(value)'\'''`
	if toolCall.Kind != agenttypes.ToolKindExecute ||
		toolCall.Title != wantCommand ||
		toolCall.Meta["command"] != wantCommand ||
		toolCall.Meta["tool"] != "exec_command" {
		t.Fatalf("exec tool call = %#v", toolCall)
	}
}

func TestParseImportedCodexToolCallClassifiesWrappedTools(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantKind  agenttypes.ToolKind
		wantTitle string
		wantPath  string
	}{
		{
			name:      "web search",
			input:     `const r = await tools.web__run({search_query:[{q:"DeepSeek Harness ACP"}],response_length:"long"}); text(r);`,
			wantKind:  agenttypes.ToolKindWebSearch,
			wantTitle: "DeepSeek Harness ACP",
		},
		{
			name:      "patch variable",
			input:     `const patch = "*** Begin Patch\n*** Update File: server/main.go\n@@\n-const tool = tools.exec_command\n+const tool = realCommand\n*** End Patch"; text(await tools.apply_patch(patch));`,
			wantKind:  agenttypes.ToolKindEdit,
			wantTitle: "main.go",
			wantPath:  "server/main.go",
		},
		{
			name:      "write stdin",
			input:     `const r = await tools.write_stdin({session_id:33334,chars:"",yield_time_ms:30000}); text(r);`,
			wantKind:  agenttypes.ToolKindExecute,
			wantTitle: "write_stdin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCall, ok := parseImportedCodexToolCall(map[string]any{
				"type":    "custom_tool_call",
				"name":    "exec",
				"call_id": "call-1",
				"input":   tt.input,
			}, 0)
			if !ok {
				t.Fatal("parseImportedCodexToolCall returned false")
			}
			if toolCall.Kind != tt.wantKind || toolCall.Title != tt.wantTitle || toolCall.Meta["tool"] == nil {
				t.Fatalf("tool call = %#v", toolCall)
			}
			if tt.wantPath != "" && (len(toolCall.Locations) != 1 || toolCall.Locations[0].Path != tt.wantPath) {
				t.Fatalf("locations = %#v, want %q", toolCall.Locations, tt.wantPath)
			}
		})
	}
}

func TestImportedCodexToolOutputFlattensExecContentBlocks(t *testing.T) {
	raw := []any{
		map[string]any{
			"type": "input_text",
			"text": "Script completed\nWall time 0.8 seconds\nOutput:\n",
		},
		map[string]any{
			"type": "input_text",
			"text": "ok  \tmindfs/server/internal/agent/codex\t0.127s\n" +
				"ok  \tmindfs/server/internal/agent/claude\t(cached)\n",
		},
	}
	output, failed := importedCodexToolOutput(raw)
	if failed {
		t.Fatal("output unexpectedly marked failed")
	}
	output = cleanImportedCodexExecOutput(output)
	want := "ok  \tmindfs/server/internal/agent/codex\t0.127s\n" +
		"ok  \tmindfs/server/internal/agent/claude\t(cached)"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}
