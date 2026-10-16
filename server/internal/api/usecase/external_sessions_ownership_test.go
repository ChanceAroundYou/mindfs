package usecase

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// live-owned 会话（MindFS 自己驱动）：导入器不做常规增量同步，只在进程启动后允许一次
// 兜底补齐；用户手点「同步」（Full）始终可用。这是"谁驱动，谁落盘"的写入侧守卫。
func TestSyncExternalSessionDeltaSkipsLiveOwnedSession(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "LiveOwned"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	liveCtx := session.WithExchangeSource(context.Background(), session.ExchangeSourceLive)
	if err := manager.AddExchangeForAgentAt(liveCtx, created, "user", "old", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(context.Background(), current, "codex", 1, "external-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		{Role: "user", Content: "from transcript", Timestamp: stamp.Add(time.Minute)},
	}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}

	// 常规增量同步（非 Full）：一律拦下，**没有**启动例外。曾经的「进程启动后补一次」
	// 在 2026-09-16 被真实事故证伪：冻结游标会把早已落库的旧回合整段重导。
	externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, created.Key), time.Now().Add(-time.Hour))
	if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{RootID: root.ID, Key: created.Key}); err != nil {
		t.Fatal(err)
	}
	if importer.calls != 0 {
		t.Fatalf("live-owned 会话的常规增量同步应当一律拦下，实际读了转录 %d 次", importer.calls)
	}

	// 用户手点「同步」：始终允许
	externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, created.Key), time.Now().Add(-time.Hour))
	if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{RootID: root.ID, Key: created.Key, Full: true}); err != nil {
		t.Fatal(err)
	}
	if importer.calls == 0 {
		t.Fatalf("用户手点同步（Full）必须仍然可用")
	}
}

// 纯导入会话（从未被 MindFS 驱动）不受守卫影响：导入器照常跟进。
func TestSyncExternalSessionDeltaKeepsImportOwnedSession(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "ImportOwned"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	importCtx := session.WithExchangeSource(context.Background(), session.ExchangeSourceImport)
	if err := manager.AddExchangeForAgentAt(importCtx, created, "user", "old", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(context.Background(), current, "codex", 1, "external-2"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		{Role: "user", Content: "next from transcript", Timestamp: stamp.Add(time.Minute)},
	}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	for attempt := 0; attempt < 2; attempt++ {
		externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, created.Key), time.Now().Add(-time.Hour))
		importer.calls = 0
		if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{RootID: root.ID, Key: created.Key}); err != nil {
			t.Fatal(err)
		}
		if importer.calls == 0 {
			t.Fatalf("第 %d 次：纯导入会话必须继续被同步", attempt+1)
		}
	}
}

// 手动「同步」（Full）在 live-owned 会话上依然可用：子代理的转录（含 user 行与多回合）
// 会被完整导进来。这正是"实时路径看不到的子代理内容不会永久失去"的证据。
func TestSyncExternalSessionDeltaFullImportsSubagentTurns(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	ctx := context.Background()
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	liveCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	if err := manager.AddExchangeForAgentAt(liveCtx, parent, "user", "parent turn", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, parent.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, current, "codex", 1, "external-parent"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{subagents: []agenttypes.ImportedSubagentSession{{
		AgentSessionID:   "claude-subagent:abc",
		Title:            "Child",
		ParentToolCallID: "call-1",
		Exchanges: []agenttypes.ImportedExchange{
			// 实时路径从不写子代理的 user 行（工具结果 / 系统注入），只有转录有
			{Role: "user", Content: "tool result", Timestamp: stamp},
			{Role: "agent", Content: "child turn 1", Timestamp: stamp.Add(time.Second)},
			{Role: "agent", Content: "child turn 2", Timestamp: stamp.Add(2 * time.Second)},
		},
	}}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{RootID: root.ID, Key: parent.Key, Full: true}); err != nil {
		t.Fatal(err)
	}
	children, err := manager.List(ctx, session.ListOptions{ParentSessionKey: parent.Key, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Fatalf("子会话数 = %d, want 1", len(children))
	}
	child, err := manager.Get(ctx, children[0].Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, exchange := range child.Exchanges {
		roles[exchange.Role]++
	}
	if roles["user"] != 1 || roles["agent"] != 2 {
		t.Fatalf("子会话行构成 = %v, want {user:1 agent:2}（多回合 + user 行都应当被同步进来）", roles)
	}
}

// live-owned 会话的导入必须带时间地板：游标是冻结的旧值，只按偏移读会把早就落库的回合
// 整段重导（2026-09-16 BP 会话的真实事故）。import-owned 会话不受影响。
func TestSyncExternalSessionDeltaSetsFloorForLiveOwnedOnly(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	ctx := context.Background()
	stamp := time.Date(2026, 9, 15, 12, 47, 25, 0, time.UTC)

	liveOwned, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Live"})
	if err != nil {
		t.Fatal(err)
	}
	liveCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	if err := manager.AddExchangeForAgentAt(liveCtx, liveOwned, "agent", "实时写的末尾一行", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	importOwned, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Import"})
	if err != nil {
		t.Fatal(err)
	}
	importCtx := session.WithExchangeSource(ctx, session.ExchangeSourceImport)
	if err := manager.AddExchangeForAgentAt(importCtx, importOwned, "agent", "导入写的末尾一行", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{liveOwned.Key, importOwned.Key} {
		current, err := manager.Get(ctx, key, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.UpdateAgentState(ctx, current, "codex", len(current.Exchanges), "external-"+key[:4]); err != nil {
			t.Fatal(err)
		}
	}

	importer := &syncDeltaTestImporter{}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}

	externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, liveOwned.Key), time.Now().Add(-time.Hour))
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{RootID: root.ID, Key: liveOwned.Key, Full: true}); err != nil {
		t.Fatal(err)
	}
	if !importer.input.TimestampFloor.Equal(stamp) {
		t.Fatalf("live-owned 会话的地板 = %v，want %v（库里最新一条的时间戳）", importer.input.TimestampFloor, stamp)
	}

	externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, importOwned.Key), time.Now().Add(-time.Hour))
	importer.input = agenttypes.ImportExternalSessionInput{}
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{RootID: root.ID, Key: importOwned.Key, Full: true}); err != nil {
		t.Fatal(err)
	}
	if !importer.input.TimestampFloor.IsZero() {
		t.Fatalf("import-owned 会话不该带地板，得到 %v", importer.input.TimestampFloor)
	}
}
