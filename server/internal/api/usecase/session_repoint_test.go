package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
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
