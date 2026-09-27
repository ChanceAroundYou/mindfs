package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func seedRepointSession(t *testing.T, manager *Manager) string {
	t.Helper()
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "claude", Name: "R"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := manager.UpsertAgentBinding(context.Background(), AgentBinding{
		SessionKey:     created.Key,
		Agent:          "claude",
		AgentSessionID: "old-agent-session-id",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return created.Key
}

// repoint 的核心不变式：换 id 和推进游标必须**同时**成立。
// 只满足其一时，下游导入器要么找不到转录，要么从 committed=0 整份重读
// （2026-09-16 重复渲染的成因）。所以两条断言要一起看，不能只测其中一条。
func TestRepointAgentBindingSwapsIDAndCursorTogether(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	key := seedRepointSession(t, manager)

	newID := "new-agent-session-id"
	newPath := filepath.Join(t.TempDir(), "projects", "main-slug", newID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const eof = int64(7_699_287)
	if err := manager.RepointAgentBinding(key, "claude", "old-agent-session-id", newID, agenttypes.ExternalSessionCursor{
		SourcePath:      newPath,
		Offset:          eof,
		ModTimeUnixNano: 12345,
		CommittedOffset: eof,
	}); err != nil {
		t.Fatalf("repoint binding: %v", err)
	}

	binding, err := manager.GetAgentBinding(context.Background(), key, "claude")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.AgentSessionID != newID {
		t.Fatalf("agent_session_id = %q, want %q", binding.AgentSessionID, newID)
	}
	if binding.ExternalSourcePath != newPath {
		t.Fatalf("cursor path = %q, want %q", binding.ExternalSourcePath, newPath)
	}
	// committed 必须是 EOF：0 会让导入器从 0 重读整份转录。
	if binding.ExternalSourceCommittedOffset != eof {
		t.Fatalf("committed offset = %d, want EOF %d; 0 would re-import the whole transcript", binding.ExternalSourceCommittedOffset, eof)
	}
	if binding.ExternalSourceOffset != eof {
		t.Fatalf("offset = %d, want %d", binding.ExternalSourceOffset, eof)
	}
	if binding.ExternalSourceMtimeNS != 12345 {
		t.Fatalf("mtime = %d, want 12345", binding.ExternalSourceMtimeNS)
	}
}

// repoint 是重放型操作（用户手点两次、网络重试）。跑第二遍不能把游标打回 0。
func TestRepointAgentBindingIsReplaySafe(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	key := seedRepointSession(t, manager)

	cursor := agenttypes.ExternalSessionCursor{
		SourcePath:      "/tmp/x/new.jsonl",
		Offset:          4242,
		ModTimeUnixNano: 999,
		CommittedOffset: 4242,
	}
	if err := manager.RepointAgentBinding(key, "claude", "old-agent-session-id", "id-1", cursor); err != nil {
		t.Fatalf("first repoint: %v", err)
	}
	// 第二次带着更新的 EOF 重放（转录又长了几条）。
	cursor.Offset, cursor.CommittedOffset = 8000, 8000
	if err := manager.RepointAgentBinding(key, "claude", "old-agent-session-id", "id-1", cursor); err != nil {
		t.Fatalf("replay repoint: %v", err)
	}
	binding, err := manager.GetAgentBinding(context.Background(), key, "claude")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.ExternalSourceCommittedOffset != 8000 {
		t.Fatalf("committed offset after replay = %d, want 8000", binding.ExternalSourceCommittedOffset)
	}
	if binding.AgentSessionID != "id-1" {
		t.Fatalf("agent_session_id after replay = %q, want id-1", binding.AgentSessionID)
	}
}

// 名字必须跟着新 id 走：重开会话时按 agent_session_id 查别名，查不到就会退回默认名。
func TestRepointAgentBindingKeepsSessionName(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	key := seedRepointSession(t, manager)

	if _, err := manager.Rename(context.Background(), key, "重命名后的名字"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := manager.RepointAgentBinding(key, "claude", "old-agent-session-id", "id-new", agenttypes.ExternalSessionCursor{
		SourcePath: "/tmp/x/new.jsonl", Offset: 10, ModTimeUnixNano: 1, CommittedOffset: 10,
	}); err != nil {
		t.Fatalf("repoint binding: %v", err)
	}
	name, ok := manager.LookupAliasForAgent("claude", "id-new")
	if !ok {
		t.Fatalf("no alias for the new agent_session_id; the session would reopen under a default name")
	}
	if name != "重命名后的名字" {
		t.Fatalf("alias for new id = %q, want %q", name, "重命名后的名字")
	}
	// 旧 id 必须查不到名字了：两份都留着的话，重开时可能命中错的那条。
	if old, stillThere := manager.LookupAliasForAgent("claude", "old-agent-session-id"); stillThere {
		t.Fatalf("alias still resolves for the old id (%q); the stale id could win a lookup", old)
	}
}

// ctx_seq 是 Full 同步的幂等护栏，repoint 必须原样保留它。
//
// 它挡的是这条路：点「同步」时 externalSessionDeltaAfterCtxSeq 用 ctx_seq 切掉库里已有的
// 前缀，只补新增部分。写 0 会命中 `agentCtxSeq <= 0 → return exchanges` 直接放行全部，
// 于是下一次点同步就把早已渲染过的回合重导一遍 —— 202609-16「ask 下面又渲染了一轮出现过的
// 文字」。实测复现：repoint 后第一次同步多导 2 条（20 → 22 行），第二次才是 0。
func TestRepointAgentBindingPreservesCtxSeq(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	key := seedRepointSession(t, manager)

	// 会话已经跑过 20 轮：ctx_seq 推进到 20。
	if err := manager.UpdateAgentState(context.Background(), mustGetSession(t, manager, key), "claude", 20, "old-agent-session-id"); err != nil {
		t.Fatalf("update agent state: %v", err)
	}

	if err := manager.RepointAgentBinding(key, "claude", "old-agent-session-id", "id-new", agenttypes.ExternalSessionCursor{
		SourcePath: "/tmp/x/new.jsonl", Offset: 10, ModTimeUnixNano: 1, CommittedOffset: 10,
	}); err != nil {
		t.Fatalf("repoint binding: %v", err)
	}

	binding, err := manager.GetAgentBinding(context.Background(), key, "claude")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.AgentCtxSeq != 20 {
		t.Fatalf("agent_ctx_seq after repoint = %d, want 20; 0 disables the full-sync dedup guard "+
			"and the next sync re-imports already-rendered turns", binding.AgentCtxSeq)
	}
}

func mustGetSession(t *testing.T, manager *Manager, key string) *Session {
	t.Helper()
	current, err := manager.Get(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return current
}
