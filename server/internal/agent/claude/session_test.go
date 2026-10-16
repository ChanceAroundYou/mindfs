package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	"mindfs/server/internal/agent/types"
)

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

func TestClaudePlanToolsMapToThinkKind(t *testing.T) {
	for _, name := range []string{"Think", "UpdatePlan", "update_plan", "EnterPlanMode", "ExitPlanMode"} {
		if got := mapToolKind(name); got != types.ToolKindThink {
			t.Fatalf("mapToolKind(%q) = %q, want think", name, got)
		}
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
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "of[1M]",
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
		{"fable alias -> fable env", "fable", fullEnv, "fable"},
		{"fable display of -> fable env", "of", fullEnv, "fable"},
		{"fable display of with 1m -> fable alias with 1m", "of[1m]", fullEnv, "fable[1m]"},
		{"opus short env pin -> API alias", "op", map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "op[1M]"}, "opus"},
		{"sonnet short env pin -> API alias", "os", map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "os"}, "sonnet"},
		{"haiku short env pin -> API alias", "ok", map[string]string{"ANTHROPIC_DEFAULT_HAIKU_MODEL": "ok"}, "haiku"},
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
	if got := claudeEffectiveEnv(baseEnv, "")["MINDTEST_PROJECT"]; got != "" {
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
