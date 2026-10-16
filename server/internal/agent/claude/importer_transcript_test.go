package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

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
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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

func TestReadClaudeImportedExchangesDropsInterruptMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// CLI 把「用户按了中断」写成一条独立 user 条目（29 字、isMeta 为 None，所以
	// isMeta 过滤拦不住），下一条才是真人正文；导入器的「相邻同角色合并」会把两者
	// 粘成 '[Request interrupted by user]\n\n正文'。正文此前已被实时路径写过一次，
	// 而粘连版多 31 字前缀、既非相等也非前缀包含，判重放行 → 落成第二条。
	// 实测 2026-09-14 17:21 的本会话：seq 136（正文）与 seq 138（标记+正文）并存。
	content := `{"type":"user","uuid":"u0","timestamp":"2026-09-14T09:21:03Z","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}
{"type":"user","uuid":"u1","timestamp":"2026-09-14T09:21:45Z","message":{"content":[{"type":"text","text":"先把 v1 那三个数字反推一下"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-14T09:21:52Z","message":{"content":[{"type":"text","text":"好。"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Content != "先把 v1 那三个数字反推一下" {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("中断标记不该被并进正文, got %v", got)
	}
	for _, item := range items {
		if strings.Contains(item.Content, "[Request interrupted") {
			t.Fatalf("标记应整条不发射: %q", item.Content)
		}
	}
}

func TestReadClaudeImportedExchangesDropsTaskNotification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// CLI 的子代理完成通知：转录里是一条 role=user、isMeta 为空的整块条目，正文是
	// 子代理的报告。子代理自己的转录已经作为子会话导入（parent_tool_call_id 指向
	// Task 调用），这条再落库就是「用户气泡里装着助手正文」的乱格式块。
	// 实测 AIS/docs 的 BP 会话里有 38 条，最后一条 10704 字。
	content := `{"type":"user","uuid":"u1","timestamp":"2026-09-14T11:03:23Z","message":{"content":[{"type":"text","text":"康复侧的市场你去做查询"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-14T11:06:08Z","message":{"content":[{"type":"text","text":"我派研究员去查。"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-14T11:09:18Z","message":{"content":[{"type":"text","text":"<task-notification>\n<task-id>a34e59580e68c4492</task-id>\n<status>completed</status>\n<summary>Agent \"研究支具佩戴周期与费用\" finished</summary>\n<result>## 1. 佩戴时长\n\n结论：2-4 年。\n</result>\n<usage><subagent_tokens>0</subagent_tokens></usage>\n</task-notification>"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-14T11:11:02Z","message":{"content":[{"type":"text","text":"报告回来了，重跑模型。"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 通知整条不发射；前后两条助手正文照常按「相邻同角色合并」并成一条。
	if len(items) != 2 || items[0].Role != "user" || items[0].Content != "康复侧的市场你去做查询" ||
		items[1].Role != "agent" || !strings.Contains(items[1].Content, "报告回来了，重跑模型。") {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("task-notification 不该落成用户气泡, got %v", got)
	}
	for _, item := range items {
		if strings.Contains(item.Content, "<task-notification>") {
			t.Fatalf("通知正文不得出现在会话里: %q", item.Content)
		}
	}
}

// 同一件事的外形之二：后台任务事件被 CLI 包上「SYSTEM NOTIFICATION - NOT USER INPUT」抬头
// （抬头自己就写明不是用户输入），下面才是 task-notification 块。
func TestReadClaudeImportedExchangesDropsSystemNotificationPreamble(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","uuid":"u1","timestamp":"2026-09-14T11:03:23Z","message":{"content":[{"type":"text","text":"跑个后台任务"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-14T11:05:00Z","message":{"content":[{"type":"text","text":"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event, NOT a message from the user.\nDo NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n\n<task-notification>\n<task-id>b1g0vjcvo</task-id>\n<status>completed</status>\n<summary>Background command \"ls\" completed (exit code 0)</summary>\n</task-notification>"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-14T11:06:00Z","message":{"content":[{"type":"text","text":"跑完了。"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Role != "user" || items[0].Content != "跑个后台任务" ||
		items[1].Role != "agent" {
		got := make([]string, 0, len(items))
		for _, item := range items {
			got = append(got, item.Role+":"+item.Content)
		}
		t.Fatalf("SYSTEM NOTIFICATION 抬头不该落成用户气泡, got %v", got)
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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

	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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
	items, _, err := readClaudeImportedExchanges(path, 0, time.Time{}, time.Time{})
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
