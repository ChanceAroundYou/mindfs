package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

// 转录条目工厂：只写 fork 定位用得到的字段，其余保持缺省。
func writeForkFixtureEntry(t *testing.T, path string, entry map[string]any) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer fh.Close()
	if _, err := fh.Write(append(data, '\n')); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func assistantForkEntry(uuid, ts, text string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"uuid":      uuid,
		"timestamp": ts,
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

func userForkEntry(uuid, ts, text string, meta bool) map[string]any {
	entry := map[string]any{
		"type":      "user",
		"uuid":      uuid,
		"timestamp": ts,
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
	if meta {
		entry["isMeta"] = true
	}
	return entry
}

// 复现 2026-11-20 的现场：MindFS 的助手行序与导入器的 turn 切分不是一一对应
// （中间那条 CLI 注入的用户条目被丢弃 → 两个助手回合被合并），于是按序号 fork
// 会越界 / 指错位置，而按目标行的落盘时间可以稳定命中正确的那条助手消息。
func TestClaudeForkResolvesByTimestampWhenTurnIndexDrifts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeForkFixtureEntry(t, path, userForkEntry("u1", "2026-11-08T15:08:00Z", "第一个问题", false))
	writeForkFixtureEntry(t, path, assistantForkEntry("a1", "2026-11-08T15:08:06Z", "第一个回答"))
	writeForkFixtureEntry(t, path, userForkEntry("u-meta", "2026-11-08T15:10:00Z", "This session was migrated from elsewhere.", false))
	writeForkFixtureEntry(t, path, assistantForkEntry("a2", "2026-11-08T15:17:28Z", "第二个回答"))
	writeForkFixtureEntry(t, path, userForkEntry("u2", "2026-11-08T15:20:00Z", "第二个问题", false))
	writeForkFixtureEntry(t, path, assistantForkEntry("a3", "2026-11-08T15:25:00Z", "第三个回答"))

	// 导入器把 a1/a2 合并成一个 turn：MindFS 侧第 2 行（a2）在转录里没有独立 turn。
	items, _, err := readClaudeImportedExchangeLocators(path, 0, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("read locators: %v", err)
	}
	if turns := buildImportedTurns(items); len(turns) != 2 {
		t.Fatalf("turns = %d, want 2（a1/a2 合并后）", len(turns))
	}

	// 旧口径：按 MindFS 行号取第 2 个 turn 会落在 a3（越过了目标轮），第 3 个直接越界。
	if uuid, err := findClaudeMessageUUIDAtOrBefore(path, mustParseForkTime(t, "2026-11-08T15:17:29Z")); err != nil || uuid != "a2" {
		t.Fatalf("timestamp resolve = (%q, %v), want a2", uuid, err)
	}
	// 边界：目标行正好落在助手条目时间戳之前（live 行落盘早于消息写的极端情况）时，
	// 退回上一条而不是报错 —— 与「最后一条不晚于目标时间」的口径一致。
	if uuid, err := findClaudeMessageUUIDAtOrBefore(path, mustParseForkTime(t, "2026-11-08T15:17:00Z")); err != nil || uuid != "a1" {
		t.Fatalf("before-turn resolve = (%q, %v), want a1", uuid, err)
	}
	if _, err := findClaudeMessageUUIDAtOrBefore(path, mustParseForkTime(t, "2026-11-08T15:00:00Z")); !errors.Is(err, errClaudeForkMessageBeforeTimestamp) {
		t.Fatalf("no-message error = %v, want errClaudeForkMessageBeforeTimestamp", err)
	}
}

// 子代理条目（isSidechain）不能成为 fork 点：fork 到它等于把子代理上下文当主会话历史。
// 同一条助手消息被 CLI 重放成新时间戳的新 uuid 时，应当命中「不晚于目标时间」的那一份。
func TestClaudeForkTimestampSkipsSidechainAndPicksLastReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeForkFixtureEntry(t, path, assistantForkEntry("a1", "2026-11-08T15:08:06Z", "主回答"))
	sidechain := assistantForkEntry("side", "2026-11-08T15:09:00Z", "子代理回答")
	sidechain["isSidechain"] = true
	writeForkFixtureEntry(t, path, sidechain)
	writeForkFixtureEntry(t, path, assistantForkEntry("replay", "2026-11-08T15:42:48Z", "主回答"))

	if uuid, err := findClaudeMessageUUIDAtOrBefore(path, mustParseForkTime(t, "2026-11-08T15:10:00Z")); err != nil || uuid != "a1" {
		t.Fatalf("sidechain 前的解析 = (%q, %v), want a1", uuid, err)
	}
	if uuid, err := findClaudeMessageUUIDAtOrBefore(path, mustParseForkTime(t, "2026-11-08T15:50:00Z")); err != nil || uuid != "replay" {
		t.Fatalf("重放后的解析 = (%q, %v), want replay", uuid, err)
	}
}

// 解析器入口必须真的用上 TargetTimestamp：索引 3 在转录里越界，但目标时间戳指向第 2 个
// 助手消息，应当成功返回它的 uuid。
func TestClaudeResolveForkPointPrefersTargetTimestamp(t *testing.T) {
	projectsDir := withForkFixtureHome(t)
	rootPath := "/home/xiaokubao/projects/fork-fixture"
	sessionID := "11111111-2222-3333-4444-555555555555"
	dir := filepath.Join(projectsDir, claudeProjectDirName(rootPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	for _, entry := range []map[string]any{
		withForkIdentity(userForkEntry("u1", "2026-11-08T15:08:00Z", "第一个问题", false), rootPath, sessionID),
		withForkIdentity(assistantForkEntry("a1", "2026-11-08T15:08:06Z", "第一个回答"), rootPath, sessionID),
		withForkIdentity(userForkEntry("u-meta", "2026-11-08T15:10:00Z", "This session was migrated from elsewhere.", false), rootPath, sessionID),
		withForkIdentity(assistantForkEntry("a2", "2026-11-08T15:17:28Z", "第二个回答"), rootPath, sessionID),
		withForkIdentity(userForkEntry("u2", "2026-11-08T15:20:00Z", "第二个问题", false), rootPath, sessionID),
		withForkIdentity(assistantForkEntry("a3", "2026-11-08T15:25:00Z", "第三个回答"), rootPath, sessionID),
	} {
		writeForkFixtureEntry(t, path, entry)
	}

	imp := NewImporter(ImporterOptions{AgentName: "claude"})
	ctx := context.Background()
	if _, err := imp.scanSessionFiles(ctx, rootPath, time.Time{}, time.Time{}, 10, nil); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// 旧路径：MindFS 数出 3 行，转录只有 2 个 turn → 越界。
	if _, err := imp.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
		RootPath:       rootPath,
		AgentSessionID: sessionID,
		AgentTurnIndex: 3,
	}); err == nil {
		t.Fatal("index=3 应当复现 agent turn index out of range")
	}

	out, err := imp.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
		RootPath:        rootPath,
		AgentSessionID:  sessionID,
		AgentTurnIndex:  3,
		TargetTimestamp: mustParseForkTime(t, "2026-11-08T15:17:29Z"),
	})
	if err != nil {
		t.Fatalf("timestamp resolve failed: %v", err)
	}
	if out.Kind != agenttypes.ForkPointClaudeMessageUUID || out.ClaudeMessageUUID != "a2" || out.AgentSessionID != sessionID {
		t.Fatalf("fork point = %#v, want a2", out)
	}
}

func withForkIdentity(entry map[string]any, cwd, sessionID string) map[string]any {
	entry["cwd"] = cwd
	entry["sessionId"] = sessionID
	return entry
}

func mustParseForkTime(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse time %q: %v", raw, err)
	}
	return parsed
}

// fork 定位与导入共用同一个 HOME 口径的转录目录，隔离到临时目录。
func withForkFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".claude", "projects")
}
