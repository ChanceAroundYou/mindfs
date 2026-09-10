package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
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

func TestReadClaudeImportedExchangesDedupesRepeatedUUIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// 转录里同一批条目可能整段重复出现（实测某会话 1712/6336 条 uuid 重复，重复区间
	// 相隔上万行）。这些重复相隔很远，无法被「相邻同角色才合并」吃掉，必须按 uuid 去重，
	// 否则同一段助手文本会被落库两次以上。
	content := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"question"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"answer"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:16:00Z","message":{"content":[{"type":"text","text":"next"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:16:05Z","message":{"content":[{"type":"text","text":"second"}]}}
{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"question"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"answer"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:16:00Z","message":{"content":[{"type":"text","text":"next"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:16:05Z","message":{"content":[{"type":"text","text":"second"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("len(items) = %d, want 4 (重复 uuid 必须去重): %v", len(items), got)
	}
	for i, want := range []string{"question", "answer", "next", "second"} {
		if items[i].Content != want {
			t.Fatalf("items[%d].Content = %q, want %q", i, items[i].Content, want)
		}
	}
}

func TestReadClaudeImportedExchangesDedupesUUIDsWithToolResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// 重复区块里的 tool_result 也不得被二次应用（否则同一 aux 会被重复挂到工具面板）。
	content := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"run it"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:01Z","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:15:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}
{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"run it"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:01Z","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:15:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (user + 带 aux 的 agent)", len(items))
	}
	if items[0].Role != "user" || items[0].Content != "run it" {
		t.Fatalf("items[0] = %#v", items[0])
	}
	if items[1].Role != "agent" || len(items[1].Aux) != 1 {
		t.Fatalf("items[1] = %#v, want 恰好 1 个 aux（tool_result 不得二次应用）", items[1])
	}
}

func TestReadClaudeImportedExchangesIgnoresUnsupportedToolCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","uuid":"u1","timestamp":"2026-07-28T01:00:00Z","message":{"content":[{"type":"text","text":"inspect README"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-28T01:00:01Z","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Read","input":{"file_path":"README.md"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-07-28T01:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"README contents"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-07-28T01:00:03Z","message":{"content":[{"type":"text","text":"done"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %#v", len(items), items)
	}
	if items[0].Role != "user" || items[0].Content != "inspect README" {
		t.Fatalf("user exchange = %#v", items[0])
	}
	if items[1].Role != "agent" || items[1].Content != "done" || len(items[1].Aux) != 0 {
		t.Fatalf("assistant exchange = %#v, want text without aux", items[1])
	}
}

func TestReadClaudeImportedExchangesMarksFailedToolResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","uuid":"u1","timestamp":"2026-07-28T01:00:00Z","message":{"content":"run"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-28T01:00:01Z","message":{"content":[{"type":"text","text":"running"},{"type":"tool_use","id":"tool-2","name":"Bash","input":{"command":"false"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-07-28T01:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-2","is_error":true,"content":"exit status 1"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || len(items[1].Aux) != 1 || items[1].Aux[0].ToolCall == nil {
		t.Fatalf("items = %#v", items)
	}
	toolCall := items[1].Aux[0].ToolCall
	if toolCall.Status != "failed" || toolCall.Kind != agenttypes.ToolKindExecute {
		t.Fatalf("failed tool call = %#v", toolCall)
	}
	if items[1].Aux[0].Line != 1 {
		t.Fatalf("tool call line = %d, want 1", items[1].Aux[0].Line)
	}
}

func TestReadClaudeImportedExchangesIncludesPlanToolCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := `{"type":"assistant","timestamp":"2026-07-28T01:00:01Z","message":{"content":[{"type":"tool_use","id":"plan-1","name":"EnterPlanMode","input":{}}]}}
{"type":"user","timestamp":"2026-07-28T01:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"plan-1","content":"entered plan mode"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Aux) != 1 || items[0].Aux[0].ToolCall == nil {
		t.Fatalf("items = %#v", items)
	}
	toolCall := items[0].Aux[0].ToolCall
	if toolCall.Kind != agenttypes.ToolKindThink || toolCall.Status != "complete" {
		t.Fatalf("plan tool call = %#v", toolCall)
	}
}

func TestReadClaudeImportedExchangesIncludesAskUserToolCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := `{"type":"assistant","timestamp":"2026-07-28T01:00:01Z","message":{"content":[{"type":"tool_use","id":"ask-1","name":"AskUserQuestion","input":{"questions":[{"question":"Continue?","options":[{"label":"Yes"}]}]}}]}}
{"type":"user","timestamp":"2026-07-28T01:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"ask-1","content":"Yes"}]},"toolUseResult":{"questions":[{"question":"Continue?"}],"answers":{"Continue?":"Yes"}}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := readClaudeImportedExchanges(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Aux) != 1 || items[0].Aux[0].ToolCall == nil {
		t.Fatalf("items = %#v", items)
	}
	toolCall := items[0].Aux[0].ToolCall
	if toolCall.Kind != agenttypes.ToolKindAskUser || toolCall.Status != "complete" {
		t.Fatalf("ask-user tool call = %#v", toolCall)
	}
	if toolCall.Meta["questionCount"] != 1 {
		t.Fatalf("ask-user meta = %#v, want question count", toolCall.Meta)
	}
	answers, _ := toolCall.Meta["answers"].(map[string]string)
	if answers["q_0"] != "Yes" {
		t.Fatalf("ask-user answers = %#v, want q_0=Yes", toolCall.Meta["answers"])
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
	items, err := readClaudeImportedSubagents(parentPath)
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

func TestClaudeProjectDirNameMatchesClaudeCodeOnDiskEncoding(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"ascii path keeps letters and digits", "/Users/Ye/Databases/L000", "-Users-Ye-Databases-L000"},
		{"cjk and space chars become dashes", "/Users/Ye/HOBBIES/260817Claude Code远程访问第三方方案", "-Users-Ye-HOBBIES-260817Claude-Code---------"},
		{"dot becomes dash", "/Users/Ye/.claude", "-Users-Ye--claude"},
		{"underscore becomes dash", "/a_b/c", "-a-b-c"},
		{"emoji becomes two dashes", "/Users/test/😀", "-Users-test---"},
		{"supplementary cjk becomes two dashes", "/Users/test/𠀀", "-Users-test---"},
		{"tailing slash stripped", "/Users/Ye/L000/", "-Users-Ye-L000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeProjectDirName(tt.path); got != tt.want {
				t.Fatalf("claudeProjectDirName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizeClaudeProjectPathTruncatesAndHashesLongPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"ascii", strings.Repeat("a", 201), strings.Repeat("a", 200) + "-rkvsv5"},
		{"utf16 hash", strings.Repeat("a", 199) + "😀", strings.Repeat("a", 199) + "--rlxqg4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeClaudeProjectPath(tt.path); got != tt.want {
				t.Fatalf("sanitizeClaudeProjectPath(long path) = %q, want %q", got, tt.want)
			}
		})
	}
}

func writeClaudeSessionJSONL(t *testing.T, dir, id, cwd string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	line := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, id, cwd)
	content := line + "\n" + `{"type":"user","message":{"content":"hello from fixture"}}` + "\n"
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// worktree 会话的转录按 spawn cwd 归档到各自的 slug 目录；导入必须扫描 <root>/.worktree/*
// 的转录目录才能命中，否则同步报 external session not found（2026-09-09 手机端同步失败根因）。
func TestImportExternalSessionScansWorktreeTranscriptDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	imp := NewImporter(ImporterOptions{AgentName: "claude"})
	mainPath := filepath.Join(home, "proj")
	wtPath := filepath.Join(mainPath, ".worktree", "task-5")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(mainPath)), "main-id", mainPath)
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(wtPath)), "wt-id", wtPath)

	out, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "wt-id",
	})
	if err != nil {
		t.Fatalf("ImportExternalSession(worktree session) failed: %v", err)
	}
	if out.AgentSessionID != "wt-id" {
		t.Fatalf("AgentSessionID = %q, want wt-id", out.AgentSessionID)
	}

	// 首次导入已把 worktree 条目写入索引；二次导入走 lookupSessionFile 快路径，
	// 宽松 cwd 匹配（worktree cwd 归属根目录）必须同样命中
	out2, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "wt-id",
	})
	if err != nil {
		t.Fatalf("ImportExternalSession(index fast path) failed: %v", err)
	}
	if out2.AgentSessionID != "wt-id" {
		t.Fatalf("AgentSessionID(index) = %q, want wt-id", out2.AgentSessionID)
	}
}

// 非 .worktree 归属的外部 cwd（其它项目）不得命中：宽松匹配仅限根目录及其 .worktree/*。
func TestImportExternalSessionStillFailsForForeignRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	imp := NewImporter(ImporterOptions{AgentName: "claude"})
	mainPath := filepath.Join(home, "proj")
	otherPath := filepath.Join(home, "other")
	writeClaudeSessionJSONL(t, filepath.Join(home, ".claude", "projects", claudeProjectDirName(otherPath)), "other-id", otherPath)
	_, err := imp.ImportExternalSession(context.Background(), agenttypes.ImportExternalSessionInput{
		RootPath:       mainPath,
		AgentSessionID: "other-id",
	})
	if err == nil || !strings.Contains(err.Error(), "external session not found") {
		t.Fatalf("expected external session not found, got err=%v", err)
	}
}

func TestCwdMatchesRoot(t *testing.T) {
	root := "/data/proj"
	cases := []struct {
		cwd  string
		want bool
	}{
		{root, true},
		{root + "/.worktree/task-5", true},
		{root + "/.worktree/session-0901-01/sub", true},
		{root + "/.worktree-evil/x", false},
		{"/elsewhere/proj", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := cwdMatchesRoot(tc.cwd, root); got != tc.want {
			t.Fatalf("cwdMatchesRoot(%q, %q) = %v, want %v", tc.cwd, root, got, tc.want)
		}
	}
}
