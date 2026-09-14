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

func TestReadClaudeImportedExchangesDropsMetaUserEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// 转录自带 isMeta 标记，标明「这条不是用户输入」（CLI 注入的 skill 正文、自动续跑、
	// 命令回显等）。实测 139712 条 user/assistant 条目里 1420 条 isMeta=true，其中约
	// 1030 条连 isMeaningfulClaudeUserText 也认不出来 —— 落库后就是用户没发过的气泡。
	content := `{"type":"user","uuid":"u1","isMeta":true,"timestamp":"2026-09-13T12:26:39Z","message":{"content":[{"type":"text","text":"Base directory for this skill: /home/xiaokubao/.claude/skills/plan-only"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-13T12:26:41Z","message":{"content":[{"type":"text","text":"怎么不继续了"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-13T12:26:48Z","message":{"content":[{"type":"text","text":"继续。"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Role != "user" || items[0].Content != "怎么不继续了" ||
		items[1].Role != "agent" || items[1].Content != "继续。" {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("isMeta 条目不该落库, got %v", got)
	}
}

func TestReadClaudeImportedExchangesDropsAutoContinuePair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// CLI 在会话空闲时会自己插一问一答。提问侧 isMeta=true 已被挡；应答侧实测
	// assistant 的 isMeta 恒为 false，必须单独挡，否则它会被当成一轮助手发言，
	// 合并进相邻文本或单独成条 —— 实测 llmux/1789241416 的 seq 58(12:26) 就排在
	// seq 57(12:34) 之后，界面上看就是时间倒挂。
	content := `{"type":"user","uuid":"u1","isMeta":true,"timestamp":"2026-09-13T12:26:39Z","message":{"content":[{"type":"text","text":"Continue from where you left off."}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-13T12:26:39Z","message":{"content":[{"type":"text","text":"No response requested."}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-13T12:26:41Z","message":{"content":[{"type":"text","text":"怎么不继续了"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-13T12:26:48Z","message":{"content":[{"type":"text","text":"继续。"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Content != "怎么不继续了" || items[1].Content != "继续。" {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("自动续跑一问一答不该落库, got %v", got)
	}
	for _, item := range items {
		if strings.Contains(item.Content, "No response requested") {
			t.Fatalf("应答不该被合并进相邻助手文本: %q", item.Content)
		}
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:15:03Z","message":{"content":[{"type":"text","text":"done"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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
{"type":"assistant","uuid":"a2","timestamp":"2026-07-28T01:00:03Z","message":{"content":[{"type":"text","text":"收尾"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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
{"type":"assistant","timestamp":"2026-07-28T01:00:03Z","message":{"content":[{"type":"text","text":"收尾"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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
{"type":"assistant","timestamp":"2026-07-28T01:00:03Z","message":{"content":[{"type":"text","text":"收尾"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{})
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

// 增量读按「已提交字节偏移」续读，只应产出新增内容。
// 判据从「item.Timestamp 是否比上次新」换成「item.StartOffset 是否在已提交位置之后」：
// 前者会被 applyClaudeToolResults 对每条 tool_result 持续改写，导致同一轮被反复当成
// 新内容落库（实测受损会话 seq 317-321 是同一轮的 6 份副本）。
func TestReadClaudeImportedExchangesIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	head := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"q1"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"r1"}]}}
`
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	first, committed, err := readClaudeImportedExchanges(path, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("首轮应产出 2 条，得到 %d", len(first))
	}
	if committed != int64(len(head)) {
		t.Fatalf("尾部已收尾时应提交到文件末尾，得到 %d（文件 %d 字节）", committed, len(head))
	}

	// 追加一轮，然后从已提交位置续读：只应拿到新增那一轮。
	tail := `{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:16:00Z","message":{"content":[{"type":"text","text":"q2"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:16:05Z","message":{"content":[{"type":"text","text":"r2"}]}}
`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(tail); err != nil {
		t.Fatal(err)
	}
	f.Close()

	inc, committed2, err := readClaudeImportedExchanges(path, committed, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 新判据下不存在「回看窗口内重复产出上一轮」：已提交的条目一律不再产出。
	var sawQ2, sawR2 int
	for _, item := range inc {
		switch strings.TrimSpace(item.Content) {
		case "q1", "r1":
			t.Fatalf("已提交过的条目不得再次产出：%q", item.Content)
		case "q2":
			sawQ2++
		case "r2":
			sawR2++
		}
	}
	if sawQ2 != 1 || sawR2 != 1 {
		t.Fatalf("增量应恰好产出新增一轮（q2=%d r2=%d），共 %d 条", sawQ2, sawR2, len(inc))
	}

	// 稳态：文件无新增时续读应为空——「不会重复追加」的依据。
	eof, committed3, err := readClaudeImportedExchanges(path, committed2, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(eof) != 0 {
		t.Fatalf("无新增时续读应为空，得到 %d 条", len(eof))
	}
	if committed3 != committed2 {
		t.Fatalf("无新增时提交位置不应变化：%d → %d", committed2, committed3)
	}
}

// 尾轮还在进行（最后一条相关条目带 tool_use，正等工具结果）时不得提交：它的内容会继续
// 变，落了就是半成品，而且下次同步会把它当成新内容再落一遍。这是「同一轮落库 6 次」的
// 直接回归测试。
func TestReadClaudeImportedExchangesHoldsOpenTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	head := `{"type":"user","uuid":"u1","timestamp":"2026-08-27T02:15:00Z","message":{"content":[{"type":"text","text":"q1"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-08-27T02:15:08Z","message":{"content":[{"type":"text","text":"先查一下"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-08-27T02:15:09Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
`
	if err := os.WriteFile(path, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	open, committed, err := readClaudeImportedExchanges(path, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 用户条目照常提交；未收尾的助手轮留在原地。
	if len(open) != 1 || strings.TrimSpace(open[0].Content) != "q1" {
		t.Fatalf("未收尾的尾轮不应提交，只应产出用户条目，得到 %d 条", len(open))
	}
	if committed >= int64(len(head)) {
		t.Fatalf("提交位置应停在未收尾轮的起点，得到 %d（文件 %d 字节）", committed, len(head))
	}

	// 工具结果回来、助手收尾后：这一轮被完整提交一次，内容为最终态。
	done := head + `{"type":"user","uuid":"u2","timestamp":"2026-08-27T02:15:20Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-08-27T02:15:30Z","message":{"content":[{"type":"text","text":"查完了"}]}}
`
	if err := os.WriteFile(path, []byte(done), 0o600); err != nil {
		t.Fatal(err)
	}
	closed, committed2, err := readClaudeImportedExchanges(path, committed, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 {
		t.Fatalf("收尾后应恰好提交 1 条，得到 %d 条", len(closed))
	}
	got := strings.TrimSpace(closed[0].Content)
	if !strings.Contains(got, "先查一下") || !strings.Contains(got, "查完了") {
		t.Fatalf("提交内容应为该轮最终态，得到 %q", got)
	}
	if len(closed[0].Aux) != 1 {
		t.Fatalf("该轮的 aux 应只含 1 份工具调用，得到 %d 份", len(closed[0].Aux))
	}

	// 再同步一次不得重复提交。
	again, _, err := readClaudeImportedExchanges(path, committed2, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("已提交的轮次不得重复产出，得到 %d 条", len(again))
	}
}
