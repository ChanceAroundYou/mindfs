package usecase

import (
	"context"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 回归：归档的 command 会话推断不出 agent（没有绑定、转录里也没有 agent 字段），
// 维护不能把它当「agent required」错误上抛。生产上正是这一条旧会话让整批诊断与
// 死 PID 锁清理每次都在建条目阶段失败。
func TestSessionMaintenanceEntryOfAgentlessSessionIsNotAnError(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("maintenance", "maintenance", t.TempDir())
	manager := session.NewManager(root)

	item := &session.Session{Key: "1791733453-48a724d0969f", Type: session.TypeCommand}
	entry, err := sessionMaintenanceEntryOf(ctx, manager, item)
	if err != nil {
		t.Fatalf("agentless entry must not fail: %v", err)
	}
	if entry.Agent != "" {
		t.Fatalf("entry.Agent = %q, want empty", entry.Agent)
	}
	if entry.SessionKey != item.Key {
		t.Fatalf("entry.SessionKey = %q, want %q", entry.SessionKey, item.Key)
	}
}

// 回归：一条无 agent 的归档 command 会话不能阻断整批条目；其余会话照常返回。
func TestSessionMaintenanceEntriesKeepBatchWhenSessionIsAgentless(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("maintenance", "maintenance", t.TempDir())
	manager := session.NewManager(root)

	bound, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "bound", Agent: "claude"})
	if err != nil {
		t.Fatalf("create bound session: %v", err)
	}
	agentless, err := manager.Create(ctx, session.CreateInput{
		Type:  session.TypeCommand,
		Name:  "curl -fsSL https://example.invalid/install.sh | sh",
		Shell: "/usr/bin/zsh",
	})
	if err != nil {
		t.Fatalf("create agentless session: %v", err)
	}
	if _, err := manager.SetArchived(ctx, agentless.Key, true); err != nil {
		t.Fatalf("archive agentless session: %v", err)
	}

	svc := &Service{Registry: &commandTestRegistry{root: root, manager: manager}}
	entries, err := svc.sessionMaintenanceEntries(ctx, "")
	if err != nil {
		t.Fatalf("agentless session must not abort the batch: %v", err)
	}

	agents := make(map[string]string, len(entries))
	for _, entry := range entries {
		agents[entry.SessionKey] = entry.Agent
	}
	if agent, ok := agents[agentless.Key]; !ok {
		t.Fatalf("agentless session %s missing from entries: %#v", agentless.Key, agents)
	} else if agent != "" {
		t.Fatalf("agentless session agent = %q, want empty", agent)
	}
	if agent, ok := agents[bound.Key]; !ok {
		t.Fatalf("bound session %s missing from entries: %#v", bound.Key, agents)
	} else if agent != "claude" {
		t.Fatalf("bound session agent = %q, want claude", agent)
	}
}
