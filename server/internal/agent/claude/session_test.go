package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	"mindfs/server/internal/agent/types"
)

func TestClaudeModelInfoSupportsEffortWithoutModelNameWhitelist(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	got := claudeModelInfo(claudeagent.ModelInfo{
		Value:       "custom-provider-model",
		DisplayName: "Custom Model",
	})
	if !got.SupportEffort {
		t.Fatal("custom model should support CLI effort selection")
	}
	if !reflect.DeepEqual(got.Efforts, want) {
		t.Fatalf("custom model efforts = %v, want %v", got.Efforts, want)
	}
}

func TestClaudeTokenUsageIncludesCacheReadAndCreationInLogicalInput(t *testing.T) {
	got := claudeTokenUsage(claudeagent.ResultMessage{
		Usage: &claudeagent.NonNullableUsage{
			InputTokens:              50,
			OutputTokens:             1_100,
			CacheReadInputTokens:     10_000,
			CacheCreationInputTokens: 2_350,
		},
	})
	if got == nil || got.InputTokens != 12_400 || got.OutputTokens != 1_100 {
		t.Fatalf("usage = %#v", got)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != 10_000 {
		t.Fatalf("cache read = %#v", got.CacheReadTokens)
	}
	if got.CacheWriteTokens == nil || *got.CacheWriteTokens != 2_350 {
		t.Fatalf("cache write = %#v", got.CacheWriteTokens)
	}
}

func TestAppendClaudeDeveloperInstructionsUsesCLIAppendSystemPrompt(t *testing.T) {
	options := claudeagent.DefaultOptions()
	for _, apply := range appendClaudeDeveloperInstructions(nil, "render markdown") {
		apply(&options)
	}
	if options.SystemPrompt != "" {
		t.Fatalf("custom system prompt = %q, want empty", options.SystemPrompt)
	}
	value, ok := options.ExtraArgs["append-system-prompt"]
	if !ok || value == nil || *value != "render markdown" {
		t.Fatalf("append-system-prompt extra arg = %#v", value)
	}
}

func TestClaudeListModelsResolvesOneMModelAlias(t *testing.T) {
	if got := resolveClaudeBaseAlias(strip1MSuffix("of[1m]")); got != "of" {
		t.Fatalf("base alias = %q, want of", got)
	}
	if got := resolveClaudeBaseAlias(strip1MSuffix("fable")); got != "of" {
		t.Fatalf("advertised alias = %q, want of", got)
	}
}

func TestClaudeContextWindowPrefersCurrentModelUsage(t *testing.T) {
	s := &session{model: "of[1m]"}

	s.updateContextWindow(claudeagent.ResultMessage{ModelUsage: map[string]claudeagent.ModelUsage{
		"of":     {InputTokens: 130053, OutputTokens: 1645, ContextWindow: 200000},
		"of[1m]": {InputTokens: 66991, OutputTokens: 344, ContextWindow: 1000000},
	}})

	contextWindow, err := s.ContextWindow(nil)
	if err != nil {
		t.Fatal(err)
	}
	if contextWindow.ModelContextWindow != 1000000 {
		t.Fatalf("context window = %d, want 1000000", contextWindow.ModelContextWindow)
	}
}

func TestClaudeCompactBoundaryEmitsCompactNotice(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleCompactBoundaryMessage(claudeagent.CompactBoundaryMessage{
		UUID:      "compact-1",
		SessionID: "claude-session",
		CompactMetadata: claudeagent.CompactMetadata{
			Trigger:   "auto",
			PreTokens: 1200,
		},
	})

	if got.Type != types.EventTypeCompact {
		t.Fatalf("event type = %q, want compact", got.Type)
	}
	notice, ok := got.Data.(types.CompactNotice)
	if !ok {
		t.Fatalf("event data = %T, want CompactNotice", got.Data)
	}
	if notice.ID != "compact-1" || notice.Status != "auto" || !strings.Contains(notice.Summary, "1200") {
		t.Fatalf("notice = %#v", notice)
	}
}

func TestClaudeAuthStatusEmitsToolUpdate(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleAuthStatusMessage(claudeagent.AuthStatusMessage{
		UUID:             "auth-1",
		IsAuthenticating: true,
		Output:           []string{"open browser"},
	})

	if got.Type != types.EventTypeToolUpdate {
		t.Fatalf("event type = %q, want tool update", got.Type)
	}
	toolCall, ok := got.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", got.Data)
	}
	if toolCall.RawType != "auth_status" || toolCall.Status != "running" {
		t.Fatalf("toolCall = %#v", toolCall)
	}
}

func TestClaudePlainStreamEventFallsBackToMessageChunk(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) { got = event }}

	s.handleStreamEvent(claudeagent.StreamEvent{Type: "stream_event", Event: "delta", Delta: "hello"})

	if got.Type != types.EventTypeMessageChunk {
		t.Fatalf("event type = %q, want message chunk", got.Type)
	}
	chunk, ok := got.Data.(types.MessageChunk)
	if !ok || chunk.Content != "hello" {
		t.Fatalf("chunk = %#v", got.Data)
	}
}

func TestClaudePartialInputJSONDeltaIsKnownNonDisplayEvent(t *testing.T) {
	raw := json.RawMessage(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"具"}}`)

	textDelta, thinkingDelta := extractDeltas(raw)
	if textDelta != "" || thinkingDelta != "" {
		t.Fatalf("extractDeltas = %q, %q; want no display deltas", textDelta, thinkingDelta)
	}
	if !isKnownNonDisplayPartialEvent(raw) {
		t.Fatalf("input_json_delta should be recognized as non-display partial event")
	}
}

func TestClaudeThinkingTokensMessageUpdatesSessionIDOnly(t *testing.T) {
	events := make([]types.Event, 0, 1)
	s := &session{onUpdate: func(event types.Event) {
		events = append(events, event)
	}}

	msg := claudeagent.ThinkingTokensMessage{
		Type:                 "system",
		Subtype:              "thinking_tokens",
		EstimatedTokens:      43,
		EstimatedTokensDelta: 2,
		SessionID:            "claude-session",
	}
	s.updateSessionID(msg)

	if s.SessionID() != "claude-session" {
		t.Fatalf("session id = %q, want claude-session", s.SessionID())
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestClaudeTaskNotificationEmitsTerminalTaskUpdate(t *testing.T) {
	events := make([]types.Event, 0, 1)
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		events = append(events, event)
	}}
	s.trackTaskInfo("task-1", claudeTaskInfo{ToolUseID: "tool-1", TaskType: "local_agent"})

	s.handleTaskNotificationMessage(claudeagent.TaskNotificationMessage{
		Subtype:    "task_notification",
		TaskID:     "task-1",
		ToolUseID:  "tool-1",
		Status:     claudeagent.TaskNotificationStatusCompleted,
		OutputFile: "/tmp/claude-task-output.md",
		Summary:    "subagent finished",
	})

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one terminal update", events)
	}
	if events[0].Type != types.EventTypeToolUpdate {
		t.Fatalf("event type = %q, want tool update", events[0].Type)
	}
	toolCall, ok := events[0].Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", events[0].Data)
	}
	if toolCall.CallID != "tool-1" || toolCall.Status != "complete" {
		t.Fatalf("toolCall = %#v, want terminal update for original tool call", toolCall)
	}
	if toolCall.Meta["subtype"] != "task_notification" || toolCall.Meta["summary"] != "subagent finished" {
		t.Fatalf("meta = %#v, want notification metadata", toolCall.Meta)
	}
}

func TestClaudeLocalBashTaskLifecycleIsIgnored(t *testing.T) {
	events := make([]types.Event, 0, 3)
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		events = append(events, event)
	}}

	s.handleTaskStartedMessage(claudeagent.TaskStartedMessage{
		TaskID:      "task-1",
		ToolUseID:   "tool-1",
		TaskType:    "local_bash",
		Description: "Run shell command",
	})
	s.handleTaskProgressMessage(claudeagent.TaskProgressMessage{
		TaskID:       "task-1",
		ToolUseID:    "tool-1",
		Description:  "Run shell command",
		LastToolName: "Bash",
	})
	s.handleTaskUpdatedMessage(claudeagent.TaskUpdatedMessage{
		TaskID: "task-1",
		Patch:  claudeagent.TaskUpdatePatch{Status: claudeagent.TaskRunStatusCompleted},
	})

	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestSummarizeExecuteToolCallPrefersNaturalLanguageDescription(t *testing.T) {
	title, meta := summarizeExecuteToolCall("Bash", json.RawMessage(`{
		"command":"go test ./...",
		"description":"Run the test suite"
	}`), nil)
	if title != "Run the test suite" {
		t.Fatalf("title = %q, want tool description", title)
	}
	if meta["command"] != "go test ./..." || meta["description"] != "Run the test suite" {
		t.Fatalf("meta = %#v, want command and description", meta)
	}
}

func TestSummarizeExecuteToolCallFallsBackToNaturalLanguageTitle(t *testing.T) {
	title, meta := summarizeExecuteToolCall("Bash", json.RawMessage(`{"command":"go test ./..."}`), nil)
	if title != "Run command" {
		t.Fatalf("title = %q, want natural-language fallback", title)
	}
	if meta["command"] != "go test ./..." {
		t.Fatalf("meta = %#v, want original command in details", meta)
	}
}

func TestClaudeLocalAgentTaskProgressEmitsParentTaskUpdate(t *testing.T) {
	events := make([]types.Event, 0, 2)
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		events = append(events, event)
	}}

	s.handleTaskStartedMessage(claudeagent.TaskStartedMessage{
		TaskID:       "agent-1",
		ToolUseID:    "tool-1",
		TaskType:     "local_agent",
		Description:  "Print hi",
		SubagentType: "general-purpose",
		Prompt:       "prompt body",
	})
	s.handleTaskProgressMessage(claudeagent.TaskProgressMessage{
		TaskID:       "agent-1",
		ToolUseID:    "tool-1",
		Description:  "Print hi",
		SubagentType: "general-purpose",
	})

	if len(events) != 2 {
		t.Fatalf("events = %#v, want start and progress", events)
	}
	if events[0].Type != types.EventTypeToolCall || events[1].Type != types.EventTypeToolUpdate {
		t.Fatalf("event types = %q, %q", events[0].Type, events[1].Type)
	}
}

func TestClaudeTaskProgressDoesNotOverridePendingToolTitle(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		got = event
	}}
	s.trackPendingToolCall(types.ToolCall{
		CallID: "tool-1",
		Title:  "Print hi 5 times every 10s",
		Status: "running",
		Kind:   types.ToolKindTask,
	})

	s.handleTaskProgressMessage(claudeagent.TaskProgressMessage{
		TaskID:       "agent-1",
		ToolUseID:    "tool-1",
		Description:  "Acknowledging the user's instructions",
		SubagentType: "general-purpose",
	})

	if got.Type != types.EventTypeToolUpdate {
		t.Fatalf("event type = %q, want tool update", got.Type)
	}
	toolCall, ok := got.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", got.Data)
	}
	if toolCall.Title != "" {
		t.Fatalf("progress title = %q, want empty to preserve original title", toolCall.Title)
	}
	if toolCall.Meta["progress"] != "Acknowledging the user's instructions" {
		t.Fatalf("progress meta = %#v", toolCall.Meta)
	}
}

func TestClaudeTaskUpdatedUsesOriginalToolUseIDAndDescription(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		got = event
	}}

	s.handleTaskStartedMessage(claudeagent.TaskStartedMessage{
		TaskID:       "aab5b6559d82ef106",
		ToolUseID:    "call-task-1",
		TaskType:     "local_agent",
		Description:  "Review changed files",
		SubagentType: "general-purpose",
	})
	s.handleTaskUpdatedMessage(claudeagent.TaskUpdatedMessage{
		TaskID:  "aab5b6559d82ef106",
		Subtype: "task_updated",
		Patch:   claudeagent.TaskUpdatePatch{Status: claudeagent.TaskRunStatusCompleted},
	})

	if got.Type != types.EventTypeToolUpdate {
		t.Fatalf("event type = %q, want tool update", got.Type)
	}
	toolCall, ok := got.Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", got.Data)
	}
	if toolCall.CallID != "call-task-1" {
		t.Fatalf("callID = %q, want original tool use id", toolCall.CallID)
	}
	if toolCall.Title != "Review changed files" {
		t.Fatalf("title = %q, want original task description", toolCall.Title)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if toolCall.Meta["taskId"] != "aab5b6559d82ef106" || toolCall.Meta["parentToolUseId"] != "call-task-1" {
		t.Fatalf("meta = %#v, want task id and parent tool use id", toolCall.Meta)
	}
}

func TestClaudeTaskCreateAndUpdateToolUseMapToTaskKind(t *testing.T) {
	for _, name := range []string{"TaskCreate", "TaskUpdate", "TaskList", "TaskGet"} {
		if got := mapToolKind(name); got != types.ToolKindTask {
			t.Fatalf("mapToolKind(%q) = %q, want task", name, got)
		}
	}
}

func TestClaudePlanToolsMapToThinkKind(t *testing.T) {
	for _, name := range []string{"Think", "UpdatePlan", "update_plan", "EnterPlanMode", "ExitPlanMode"} {
		if got := mapToolKind(name); got != types.ToolKindThink {
			t.Fatalf("mapToolKind(%q) = %q, want think", name, got)
		}
	}
}

func TestClaudeTaskCreateResultUsesReturnedTaskID(t *testing.T) {
	parentID := "call-create-1"
	s := &session{}
	create := newRunningToolCall(
		parentID,
		"TaskCreate",
		"tool_use",
		json.RawMessage(`{"subject":"检查 git 状态","description":"检查当前 git 修改状态","activeForm":"检查 git 状态"}`),
	)
	create.Meta = mergeToolCallMeta(create.Meta, map[string]any{
		"taskTool":  "TaskCreate",
		"toolUseId": parentID,
	})
	s.trackPendingToolCall(create)

	update, ok := s.toolResultUpdate(claudeagent.UserMessage{
		ParentToolUseID: &parentID,
		ToolUseResult:   map[string]any{"task": map[string]any{"id": "7", "subject": "检查 git 状态"}},
	})
	if !ok {
		t.Fatal("toolResultUpdate returned ok=false")
	}
	if update.CallID != "claude-task-list:7" || update.Status != "running" {
		t.Fatalf("update = %#v, want returned task id and running status", update)
	}
	if update.Title != "检查 git 状态" || update.Meta["taskId"] != "7" || update.Meta["taskTool"] != "TaskCreate" {
		t.Fatalf("update = %#v, want create title and real task meta", update)
	}
}

func TestClaudeTaskUpdateResultPreservesTaskStatus(t *testing.T) {
	parentID := "call-update-1"
	s := &session{}
	updateBase := newRunningToolCall(
		parentID,
		"TaskUpdate",
		"tool_use",
		json.RawMessage(`{"taskId":"7","status":"completed"}`),
	)
	updateBase.Meta = mergeToolCallMeta(updateBase.Meta, map[string]any{
		"taskTool":  "TaskUpdate",
		"toolUseId": parentID,
	})
	s.trackPendingToolCall(updateBase)

	update, ok := s.toolResultUpdate(claudeagent.UserMessage{
		ParentToolUseID: &parentID,
		ToolUseResult: map[string]any{
			"success":       true,
			"taskId":        "7",
			"statusChange":  map[string]any{"from": "pending", "to": "completed"},
			"updatedFields": []any{"status"},
		},
	})
	if !ok {
		t.Fatal("toolResultUpdate returned ok=false")
	}
	if update.CallID != "claude-task-list:7" || update.Status != "complete" {
		t.Fatalf("update = %#v, want real complete task update", update)
	}
	if update.Title != "" || len(update.Content) != 0 {
		t.Fatalf("update = %#v, want status-only task update", update)
	}
	if update.Meta["taskId"] != "7" || update.Meta["taskStatus"] != "complete" {
		t.Fatalf("meta = %#v, want real task id and status", update.Meta)
	}
}

func TestSummarizeGenericToolResultContentBlocks(t *testing.T) {
	raw := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "first"},
			map[string]any{"type": "text", "text": "second"},
		},
	}

	got := summarizeGenericToolResult(raw)
	if got != "first\nsecond" {
		t.Fatalf("summarizeGenericToolResult = %q, want content block text", got)
	}
}

func TestSummarizeGenericToolResultJSONString(t *testing.T) {
	got := summarizeGenericToolResult(`{"output":"git diff output"}`)
	if got != "git diff output" {
		t.Fatalf("summarizeGenericToolResult = %q, want decoded output", got)
	}
}

func TestToolResultUpdateFallsBackToOnlyPendingTool(t *testing.T) {
	s := &session{
		pendingToolCalls: map[string]types.ToolCall{
			"call-1": {
				CallID: "call-1",
				Status: "running",
				Kind:   types.ToolKindExecute,
			},
		},
	}

	update, ok := s.toolResultUpdate(claudeagent.UserMessage{
		ToolUseResult: map[string]any{"content": "command output"},
	})
	if !ok {
		t.Fatal("toolResultUpdate returned ok=false")
	}
	if update.Status != "complete" {
		t.Fatalf("status = %q, want complete", update.Status)
	}
	if len(update.Content) != 1 || !strings.Contains(update.Content[0].Text, "command output") {
		t.Fatalf("content = %#v, want command output", update.Content)
	}
}

func TestClaudeThirdPartyModelPassthrough(t *testing.T) {
	if got := canonicalClaudeModel("deepseek-v4-pro"); got != "deepseek-v4-pro" {
		t.Fatalf("canonical third-party = %q, want deepseek-v4-pro", got)
	}
	if got := canonicalClaudeModel("DeepSeek-V4-Pro"); got != "DeepSeek-V4-Pro" {
		t.Fatalf("canonical case preserved = %q, want DeepSeek-V4-Pro", got)
	}
	if got := with1MSuffix("deepseek-v4-pro", true); got != "deepseek-v4-pro[1m]" {
		t.Fatalf("with1M third-party enabled = %q, want deepseek-v4-pro[1m]", got)
	}
	if got := with1MSuffix("deepseek-v4-pro", false); got != "deepseek-v4-pro" {
		t.Fatalf("with1M third-party disabled = %q, want deepseek-v4-pro", got)
	}
	// alias family still canonicalizes
	if got := canonicalClaudeModel("fable"); got != "of" {
		t.Fatalf("canonical fable = %q, want of", got)
	}
	if got := canonicalClaudeModel("of[1m]"); got != "of[1m]" {
		t.Fatalf("canonical of[1m] = %q, want of[1m]", got)
	}
	if got := with1MSuffix("of", true); got != "of[1m]" {
		t.Fatalf("with1M alias = %q, want of[1m]", got)
	}
	if got := with1MSuffix("os[1m]", false); got != "os" {
		t.Fatalf("with1M strip alias = %q, want os", got)
	}
	if isClaudeAliasModel("deepseek-v4-pro") {
		t.Fatalf("deepseek should not be alias model")
	}
	if isClaudeAliasModel("glm-4") || isClaudeAliasModel("glm-4-plus") {
		t.Fatalf("glm should not be alias model")
	}
	if got := canonicalClaudeModel("glm-4"); got != "glm-4" {
		t.Fatalf("canonical glm = %q, want glm-4", got)
	}
	if got := canonicalClaudeModel("GLM-4-Plus"); got != "GLM-4-Plus" {
		t.Fatalf("canonical glm case preserved = %q, want GLM-4-Plus", got)
	}
	if got := with1MSuffix("glm-4", true); got != "glm-4[1m]" {
		t.Fatalf("with1M glm enabled = %q, want glm-4[1m]", got)
	}
	if got := with1MSuffix("glm-4[1m]", false); got != "glm-4" {
		t.Fatalf("with1M glm strip = %q, want glm-4", got)
	}
	if !isClaudeAliasModel("of") || !isClaudeAliasModel("fable[1m]") {
		t.Fatalf("alias family should be detected")
	}
}

func TestResolveClaudeModelArg(t *testing.T) {
	// cc-switch 切到 DeepSeek 上游时写入的 env 形态：
	// ANTHROPIC_DEFAULT_SONNET_MODEL = "deepseek-v4-flash[1M]"，ANTHROPIC_MODEL = "sonnet"。
	fullEnv := map[string]string{
		"ANTHROPIC_MODEL":                "sonnet",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-v4-flash[1M]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "deepseek-v4-pro[1M]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "of",
	}

	cases := []struct {
		name   string
		model  string
		env    map[string]string
		expect string
	}{
		{"empty", "", fullEnv, ""},
		{"alias no toggle -> env stripped", "os", fullEnv, "deepseek-v4-flash"},
		{"alias toggle lower -> env as-is", "os[1m]", fullEnv, "deepseek-v4-flash[1M]"},
		{"alias toggle upper variant -> env as-is", "os[1M]", fullEnv, "deepseek-v4-flash[1M]"},
		{"fable alias -> fable env", "fable", fullEnv, "of"},
		{"fable display of -> fable env", "of", fullEnv, "of"},
		{"opus alias -> opus env stripped", "op", fullEnv, "deepseek-v4-pro"},
		{"default no ANTHROPIC_MODEL -> default", "default", map[string]string{}, "default"},
		{"default[1m] no env -> default toggle", "default[1m]", map[string]string{}, "default[1m]"},
		{"default -> recursive env(sonnet) result", "default", fullEnv, "deepseek-v4-flash"},
		{"default[1m] -> recursive toggle preserved", "default[1m]", fullEnv, "deepseek-v4-flash[1M]"},
		{"missing env -> fallback tier name", "op", map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "x"}, "opus"},
		{"missing env toggle -> tier name toggle", "op[1m]", map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "x"}, "opus[1m]"},
		{"third-party passthrough", "d4p", fullEnv, "d4p"},
		{"third-party with toggle -> passthrough", "d4p[1m]", fullEnv, "d4p[1m]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveClaudeModelArg(tc.model, tc.env); got != tc.expect {
				t.Fatalf("ResolveClaudeModelArg(%q) = %q, want %q", tc.model, got, tc.expect)
			}
		})
	}
}

func TestClaudeEffectiveEnv(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "user")
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(userDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	userSettings := filepath.Join(userDir, ".claude", "settings.json")
	projectSettings := filepath.Join(projectDir, ".claude", "settings.json")
	writeFile := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 用户 settings 提供 MINDTEST_USER / MINDTEST_SHARED；项目 settings 同 key 覆盖。
	writeFile(userSettings, `{"env":{"MINDTEST_USER":"user","MINDTEST_SHARED":"user"}}`)
	writeFile(projectSettings, `{"env":{"MINDTEST_USER":"project","MINDTEST_PROJECT":"project"}}`)

	t.Setenv("HOME", userDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	baseEnv := map[string]string{"MINDTEST_BASE": "base", "MINDTEST_SHARED": "base"}

	merged := claudeEffectiveEnv(baseEnv, projectDir)

	if got := merged["MINDTEST_USER"]; got != "project" {
		t.Fatalf("project should override user: got %q", got)
	}
	// 分层顺序 os.Environ → baseEnv → 用户 settings → 项目 settings，后者覆盖前者。
	if got := merged["MINDTEST_SHARED"]; got != "user" {
		t.Fatalf("user settings should override baseEnv: got %q", got)
	}
	if got := merged["MINDTEST_PROJECT"]; got != "project" {
		t.Fatalf("project settings should apply: got %q", got)
	}
	if got := merged["MINDTEST_BASE"]; got != "base" {
		t.Fatalf("baseEnv should apply: got %q", got)
	}
	// rootDir 为空时不读项目 settings。
	if got := claudeEffectiveEnv(baseEnv, "")[ "MINDTEST_PROJECT"]; got != "" {
		t.Fatalf("empty rootDir should skip project settings: got %q", got)
	}
}

// 复现 2026-09-12 症状 1 的双重发射：SDK 对同一次 AskUserQuestion 会走两条路径
// （CanUseTool → awaitAskUserQuestion，以及 AssistantMessage 的 tool_use 块），
// 两者 CallID 相同、来自不同 goroutine、次序不定。claimAskUserEmit 保证只有先到者
// 发射，后者跳过 —— 否则同一张 ask_user 卡会渲染两份。
func TestClaimAskUserEmitOnlyFirstWins(t *testing.T) {
	s := &session{}

	if !s.claimAskUserEmit("call_abc") {
		t.Fatal("first claim should win")
	}
	if s.claimAskUserEmit("call_abc") {
		t.Fatal("second claim for same callID must lose")
	}

	// 不同 callID 互不影响
	if !s.claimAskUserEmit("call_def") {
		t.Fatal("different callID should win")
	}

	// 空 callID 不做去重（无 ID 可判，放行以免丢卡）
	if !s.claimAskUserEmit("") || !s.claimAskUserEmit("") {
		t.Fatal("empty callID should always pass through")
	}
}

// 并发场景：模拟两条路径同时到达，必须恰好一个赢。
func TestClaimAskUserEmitConcurrentExactlyOneWinner(t *testing.T) {
	s := &session{}
	const racers = 8
	results := make(chan bool, racers)
	for i := 0; i < racers; i++ {
		go func() { results <- s.claimAskUserEmit("call_race") }()
	}
	winners := 0
	for i := 0; i < racers; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}

// 复现 2026-09-12 症状 1：子代理报告被当成卡片标题。
// SDK 的 TaskNotificationMessage 没有 Description 字段，只有 Summary（报告正文）。
// 旧代码把 msg.Summary 当 description 传给 claudeTaskToolCall，后者
// `title := strings.TrimSpace(description)` 于是把报告全文提升成标题，UI 上表现为
// 「一段没有思考的裸文本工具卡」，并与同 line 锚点的 ask_user 卡相邻渲染，
// 看起来像提问被重复了一遍。真正的任务描述在 trackTaskInfo 里。
func TestClaudeTaskNotificationKeepsReportOutOfTitle(t *testing.T) {
	const (
		description = "Explore sync + reload ordering"
		report      = "Now I have all the data needed to produce the final report. " +
			"Here is the complete analysis across the four areas requested."
	)
	events := make([]types.Event, 0, 1)
	s := &session{sessionID: "claude-session", onUpdate: func(event types.Event) {
		events = append(events, event)
	}}
	s.trackTaskInfo("task-1", claudeTaskInfo{
		ToolUseID:    "tool-1",
		TaskType:     "local_agent",
		Description:  description,
		SubagentType: "Explore",
	})

	s.handleTaskNotificationMessage(claudeagent.TaskNotificationMessage{
		Subtype:   "task_notification",
		TaskID:    "task-1",
		ToolUseID: "tool-1",
		Status:    claudeagent.TaskNotificationStatusCompleted,
		Summary:   report,
	})

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one terminal update", events)
	}
	toolCall, ok := events[0].Data.(types.ToolCall)
	if !ok {
		t.Fatalf("event data = %T, want ToolCall", events[0].Data)
	}
	if toolCall.Title == report || strings.Contains(toolCall.Title, "I have all the data needed") {
		t.Fatalf("title = %q, want task description, not the subagent report", toolCall.Title)
	}
	if toolCall.Title != description {
		t.Fatalf("title = %q, want %q", toolCall.Title, description)
	}
	if got := stringMeta(toolCall.Meta, "taskDescription"); got == report || got != description {
		t.Fatalf("meta.taskDescription = %q, want %q (report must not leak into it)", got, description)
	}
	// 报告本身不能丢：它应当作为正文 content 渲染。
	var body string
	for _, item := range toolCall.Content {
		body += item.Text
	}
	if !strings.Contains(body, "Here is the complete analysis") {
		t.Fatalf("content = %q, want the subagent report preserved as body", body)
	}
}
