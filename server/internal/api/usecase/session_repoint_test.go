package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

// repoint 的入参有两种形态：mindfs 的 session key（前端用），或 claude 的
// agent_session_id（wt-finish 这类脚本用 —— 它手里只有 CLAUDE_CODE_SESSION_ID）。
// 这里只测**进门校验与反查**：真正搬文件那步要动 ~/.claude/projects，不在单测里做。

type repointTestRegistry struct {
	root    rootfs.RootInfo
	manager *session.Manager
}

func (r repointTestRegistry) GetRoot(rootID string) (rootfs.RootInfo, error) {
	if rootID != r.root.ID {
		return rootfs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (r repointTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return r.manager, nil
}

func (repointTestRegistry) UpsertRoot(string) (rootfs.RootInfo, error) { return rootfs.RootInfo{}, nil }
func (repointTestRegistry) RemoveRoot(string) (rootfs.RootInfo, error) { return rootfs.RootInfo{}, nil }
func (repointTestRegistry) RenameRoot(string, string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}
func (repointTestRegistry) UpdateDisplayName(string, string) (rootfs.RootInfo, error) {
	return rootfs.RootInfo{}, nil
}
func (repointTestRegistry) ListRoots() []rootfs.RootInfo             { return nil }
func (repointTestRegistry) GetAgentPool() *agent.Pool                { return nil }
func (repointTestRegistry) GetPreferences() *preferences.Store       { return nil }
func (repointTestRegistry) GetProber() *agent.Prober                 { return nil }
func (repointTestRegistry) GetCandidateRegistry() *CandidateRegistry { return nil }
func (repointTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return nil, errors.New("not implemented")
}
func (repointTestRegistry) GetFileWatcher(string, *session.Manager) (*rootfs.SharedFileWatcher, error) {
	return nil, nil
}
func (repointTestRegistry) ReleaseFileWatcher(string, string) {}

func newRepointService(t *testing.T) (Service, *session.Manager) {
	t.Helper()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	return Service{Registry: repointTestRegistry{root: root, manager: manager}}, manager
}

func TestRepointSessionRejectsEmptyIdentifier(t *testing.T) {
	service, _ := newRepointService(t)
	_, err := service.RepointSession(context.Background(), RepointSessionInput{RootID: "mindfs"})
	if err == nil {
		t.Fatal("expected an error when neither session key nor agent session id is given")
	}
	// 脚本漏传参数时必须在这里停住，不能带着空 key 往下走。
	if !strings.Contains(err.Error(), "session key or agent session id") {
		t.Fatalf("error = %v, want it to name the missing identifier", err)
	}
}

func TestRepointSessionRejectsUnknownAgentSessionID(t *testing.T) {
	service, _ := newRepointService(t)
	_, err := service.RepointSession(context.Background(), RepointSessionInput{
		RootID:         "mindfs",
		AgentSessionID: "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956",
	})
	if err == nil {
		t.Fatal("expected an error for an agent session id no mindfs session is bound to")
	}
	// 反查失败要说清是「没绑上」，脚本据此打印「跳过」而不是当成故障。
	if !strings.Contains(err.Error(), "no mindfs session bound") {
		t.Fatalf("error = %v, want it to say no mindfs session is bound", err)
	}
}

// 关键不变式：给了 agent_session_id 时，repoint 作用的必须是**反查到的那个会话**。
// 反查错了就会把 A 会话的转录搬到 B 会话上 —— 数据看着成功、实际错位。
func TestRepointSessionResolvesAgentSessionIDToItsOwnSession(t *testing.T) {
	service, manager := newRepointService(t)
	ctx := context.Background()

	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "R"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentID = "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956"
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey: created.Key, Agent: "claude", AgentSessionID: agentID,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 只给 agent id。真正搬文件那步会失败（转录不存在），但**不能**报「找不到会话」——
	// 那说明反查没生效，脚本会误判成「不是 mindfs 的会话」而跳过。
	_, err = service.RepointSession(ctx, RepointSessionInput{RootID: "mindfs", AgentSessionID: agentID})
	if err == nil {
		t.Skip("a real transcript existed; the resolve step succeeded and there is nothing to assert here")
	}
	if strings.Contains(err.Error(), "no mindfs session bound") {
		t.Fatalf("reverse lookup failed: %v", err)
	}
	if strings.Contains(err.Error(), "session not found") {
		t.Fatalf("repoint acted on the wrong session key: %v", err)
	}
}

func TestRepointSessionTargetsClaudeBindingInMultiAgentSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	service, manager := newRepointService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Source: "worktree", Name: "R"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const claudeID = "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956"
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey: created.Key, Agent: "claude", AgentSessionID: claudeID,
	}); err != nil {
		t.Fatalf("bind claude: %v", err)
	}
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey: created.Key, Agent: "codex", AgentSessionID: "codex-thread-id",
	}); err != nil {
		t.Fatalf("bind codex: %v", err)
	}
	if err := manager.AddExchangeForAgent(ctx, created, "assistant", "latest codex exchange", "codex", "", "", ""); err != nil {
		t.Fatalf("add codex exchange: %v", err)
	}
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", "/tmp/mindfs/.worktree/task-99", "task-99", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}

	_, err = service.RepointSession(ctx, RepointSessionInput{RootID: "mindfs", AgentSessionID: claudeID})
	if err == nil || !strings.Contains(err.Error(), "找不到会话") {
		t.Fatalf("error = %v, want transcript lookup failure after resolving the Claude binding", err)
	}
	if strings.Contains(err.Error(), "only supported for claude") {
		t.Fatalf("explicit Claude session id was rejected because the latest exchange used another agent: %v", err)
	}
}

func TestRepointSessionMovesSelectedClaudeBindingWithoutChangingCodexBinding(t *testing.T) {
	claudeConfig := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	service, manager := newRepointService(t)
	ctx := context.Background()
	root := service.Registry.(repointTestRegistry).root
	rootPath, err := root.RootDir()
	if err != nil {
		t.Fatalf("root path: %v", err)
	}
	worktreePath := filepath.Join(rootPath, ".worktree", "task-99")
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Source: "worktree", Name: "R"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const claudeID = "11111111-2222-3333-4444-555555555555"
	const codexID = "codex-thread-id"
	for _, binding := range []session.AgentBinding{
		{SessionKey: created.Key, Agent: "claude", AgentSessionID: claudeID},
		{SessionKey: created.Key, Agent: "codex", AgentSessionID: codexID},
	} {
		if err := manager.UpsertAgentBinding(ctx, binding); err != nil {
			t.Fatalf("bind %s: %v", binding.Agent, err)
		}
	}
	if err := manager.AddExchangeForAgent(ctx, created, "assistant", "latest codex exchange", "codex", "", "", ""); err != nil {
		t.Fatalf("add codex exchange: %v", err)
	}
	if _, err := manager.RecordRelatedWorktree(ctx, created.Key, "mindfs", worktreePath, "task-99", "head"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	claudeBindingBefore, err := manager.FindAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatalf("find Claude binding before: %v", err)
	}
	codexBindingBefore, err := manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil {
		t.Fatalf("find Codex binding before: %v", err)
	}

	projectsDir := filepath.Join(claudeConfig, "projects")
	worktreeSlug := testClaudeProjectSlug(worktreePath)
	oldTranscript := filepath.Join(projectsDir, worktreeSlug, claudeID+".jsonl")
	writeRepointTestTranscript(t, oldTranscript, claudeID, worktreePath)

	out, err := service.RepointSession(ctx, RepointSessionInput{RootID: "mindfs", AgentSessionID: claudeID})
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}
	if out.Agent != "claude" || out.AgentSessionID == "" || out.AgentSessionID == claudeID {
		t.Fatalf("repoint output = %+v, want a new Claude transcript id", out)
	}
	if !out.WorktreeCleared {
		t.Fatalf("worktree cleared = false, output = %+v", out)
	}

	claudeBindingAfter, err := manager.FindAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatalf("find Claude binding after: %v", err)
	}
	codexBindingAfter, err := manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil {
		t.Fatalf("find Codex binding after: %v", err)
	}
	if claudeBindingAfter.AgentSessionID != out.AgentSessionID {
		t.Fatalf("Claude binding id = %q, want %q", claudeBindingAfter.AgentSessionID, out.AgentSessionID)
	}
	if claudeBindingAfter.AgentCtxSeq != claudeBindingBefore.AgentCtxSeq {
		t.Fatalf("Claude agent_ctx_seq changed from %d to %d", claudeBindingBefore.AgentCtxSeq, claudeBindingAfter.AgentCtxSeq)
	}
	if claudeBindingAfter.ExternalSourcePath != out.TranscriptPath || claudeBindingAfter.ExternalSourceOffset != claudeBindingAfter.ExternalSourceCommittedOffset {
		t.Fatalf("Claude cursor does not point at the new transcript EOF: %+v", claudeBindingAfter)
	}
	if codexBindingAfter.AgentSessionID != codexBindingBefore.AgentSessionID || codexBindingAfter.AgentCtxSeq != codexBindingBefore.AgentCtxSeq {
		t.Fatalf("Codex binding changed: before=%+v after=%+v", codexBindingBefore, codexBindingAfter)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get session after: %v", err)
	}
	if current.RelatedWorktree != nil {
		t.Fatalf("related worktree remains after repoint: %+v", current.RelatedWorktree)
	}
	if !claude.HasTranscript(out.AgentSessionID) {
		t.Fatalf("new transcript %q is not discoverable", out.AgentSessionID)
	}
}

func testClaudeProjectSlug(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, path)
}

func writeRepointTestTranscript(t *testing.T, path, sessionID, cwd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir transcript: %v", err)
	}
	entry := map[string]any{
		"type": "user", "sessionId": sessionID, "uuid": "test-message-uuid",
		"timestamp": "2026-10-13T00:00:00.000Z", "cwd": cwd,
		"message": map[string]any{"role": "user", "content": "repoint fixture"},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal transcript: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestRepointSessionRejectsKeyAndAgentSessionMismatch(t *testing.T) {
	service, manager := newRepointService(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "R"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentID = "6cfeb5a2-7b0f-4f69-90dd-9df2269a6956"
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey: created.Key, Agent: "claude", AgentSessionID: agentID,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	_, err = service.RepointSession(ctx, RepointSessionInput{
		RootID: "mindfs", Key: "another-session", AgentSessionID: agentID,
	})
	if err == nil || !strings.Contains(err.Error(), "not \"another-session\"") {
		t.Fatalf("error = %v, want mismatched key and agent session to be rejected", err)
	}
}
