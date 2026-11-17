package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 2026-11-17 事故：codex 长任务回合跑到一半时 `systemctl restart mindfs`，实时路径只写了
// 用户行和一条空的助手占位行（回合收尾时写的），已经流式显示过的正文随进程一起消失；
// 而 live-owned 会话的常规增量同步被所有权守卫挡在门外，用户看到的是「聊天记录消失，
// 刷新/重开都回不来」。这里钉的是修复：尾部**未收尾**的那一轮从转录补回助手侧内容。

func newLiveTailTestManager(t *testing.T) (*fs.RootInfo, *session.Manager) {
	t.Helper()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	return &root, manager
}

// seedInterruptedLiveTurn 造出事故里的形状：用户行（回合开始）+ 空的助手占位行（收尾
// 路径在进程退出前写的），没有任何块。
func seedInterruptedLiveTurn(t *testing.T, manager *session.Manager) (*session.Session, time.Time) {
	t.Helper()
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Interrupted"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 17, 0, 38, 3, 0, time.UTC)
	liveCtx := session.WithExchangeIdentity(
		session.WithExchangeSource(ctx, session.ExchangeSourceLive),
		session.ExchangeIdentity{MindFSID: "turn-u4", TurnSeq: 1},
	)
	if err := manager.AddExchangeForAgentAt(liveCtx, created, "user", "", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(
		session.WithExchangeIdentity(liveCtx, session.ExchangeIdentity{MindFSID: "turn-u4", TurnSeq: 1}),
		created, "agent", "", "codex", "", "", "", stamp.Add(44*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, current, "codex", 1, "thread-1"); err != nil {
		t.Fatal(err)
	}
	return current, stamp
}

func interruptedTailImporter(stamp time.Time) *syncDeltaTestImporter {
	return &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		// 导入器会把相邻同角色条目合并：四条提问并成一条巨型用户行 —— 这正是手动同步
		// 在 ask 下面渲染出重复气泡的原因，兜底路径必须把它挡在门外。
		{Role: "user", Content: "这是测试 请维持一个长周期运转的任务\n\nMindFS context: ...", Timestamp: stamp.Add(time.Second)},
		{
			Role:      "agent",
			Content:   "我会按 `using-superpowers` 的流程执行这次计时测试：每隔 5 秒输出一个递增数字。",
			Timestamp: stamp.Add(40 * time.Second),
			Aux: []agenttypes.ImportedExchangeAux{{
				Line: 0,
				ToolCall: &agenttypes.ToolCall{
					CallID: "call_1",
					Title:  "exec",
					Status: "complete",
					Kind:   agenttypes.ToolKindExecute,
				},
			}},
		},
	}}
}

func blocksForSeq(t *testing.T, manager *session.Manager, key string, seq int) []session.Block {
	t.Helper()
	blocks, err := manager.GetBlocksWindow(context.Background(), key, map[int]bool{seq: true})
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

func blocksText(blocks []session.Block) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == session.BlockTypeText {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

func runLiveTailSync(t *testing.T, svc *Service, rootID, key string) {
	t.Helper()
	// 绕开 best-effort 同步的 2s 节流（它与本用例的意图无关）。
	externalSessionSyncTimes.Store(externalSyncLockKey(rootID, key), time.Now().Add(-time.Hour))
	if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{RootID: rootID, Key: key}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncExternalSessionDeltaRecoversInterruptedLiveTail(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	importer := interruptedTailImporter(stamp)
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)

	if importer.calls != 1 {
		t.Fatalf("被中断的尾部必须从转录补一次，实际读了 %d 次", importer.calls)
	}
	// 时间地板是用户行（回合开始），不是最后一行：空的助手行是回合**收尾**时才写的，
	// 时间比转录里的回复还晚，拿它当起点会把要补的那条永远挡在外面（事故现场实测）。
	if !importer.input.AfterTimestamp.Equal(stamp) {
		t.Fatalf("AfterTimestamp = %v, want %v（用户行时间戳）", importer.input.AfterTimestamp, stamp)
	}
	if !importer.input.ForceRead {
		t.Fatal("兜底补读必须强制重读转录（live-owned 会话的字节游标是冻结的）")
	}

	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("必须复用尾部空助手行、不新增行（尤其不能追加合并后的重复用户行），实际 %d 行", len(latest.Exchanges))
	}
	text := blocksText(blocksForSeq(t, manager, current.Key, 2))
	if !strings.Contains(text, "using-superpowers") {
		t.Fatalf("恢复的正文没挂到助手行上: %q", text)
	}
	toolCalls := 0
	for _, block := range blocksForSeq(t, manager, current.Key, 2) {
		if block.Type == session.BlockTypeToolCall && block.ToolCall != nil && block.ToolCall.CallID == "call_1" {
			toolCalls++
		}
	}
	if toolCalls != 1 {
		t.Fatalf("工具卡也应随正文一起恢复，实际 %d 个", toolCalls)
	}
	binding, err := manager.FindAgentBinding(context.Background(), current.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentCtxSeq != 2 {
		t.Fatalf("AgentCtxSeq = %d, want 2（补回后与正常收尾口径一致）", binding.AgentCtxSeq)
	}

	// 再打开一次：这一轮已经有块了，不该再碰转录。
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 1 {
		t.Fatalf("补回之后不得重复导入，实际读了 %d 次", importer.calls)
	}
}

func TestSyncExternalSessionDeltaRecoversInterruptedLiveTailWithoutTrailingAgentRow(t *testing.T) {
	ctx := context.Background()
	root, manager := newLiveTailTestManager(t)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "KilledMidTurn"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 17, 0, 38, 3, 0, time.UTC)
	liveCtx := session.WithExchangeIdentity(
		session.WithExchangeSource(ctx, session.ExchangeSourceLive),
		session.ExchangeIdentity{MindFSID: "turn-only", TurnSeq: 1},
	)
	// 进程没活到回合收尾：库里只有用户行。
	if err := manager.AddExchangeForAgentAt(liveCtx, created, "user", "", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, current, "codex", 1, "thread-2"); err != nil {
		t.Fatal(err)
	}
	importer := interruptedTailImporter(stamp)
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, created.Key)

	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("应当补出一条助手行，实际 %d 行", len(latest.Exchanges))
	}
	recovered := latest.Exchanges[1]
	if recovered.Role != "agent" {
		t.Fatalf("补出来的行角色 = %q", recovered.Role)
	}
	if recovered.MindFSID != "turn-only" || recovered.TurnSeq != 1 {
		t.Fatalf("补出的助手行必须挂在用户行那一轮上（mindfs_id=%q turn_seq=%d）", recovered.MindFSID, recovered.TurnSeq)
	}
	if text := blocksText(blocksForSeq(t, manager, created.Key, recovered.Seq)); !strings.Contains(text, "using-superpowers") {
		t.Fatalf("恢复的正文没落到新助手行上: %q", text)
	}
}

func TestSyncExternalSessionDeltaSkipsFinishedLiveTail(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	// 把尾部那一轮补成「已收尾」：助手行有块流。
	if err := persistTurnBlocks(context.Background(), manager, current.Key, 2, "turn-u4", "已经收尾的回复", nil); err != nil {
		t.Fatal(err)
	}
	importer := interruptedTailImporter(stamp)
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 0 {
		t.Fatalf("已收尾的 live-owned 会话一律不自动同步（2026-09-16 重导事故的守卫），实际读了 %d 次", importer.calls)
	}
}

func TestSyncExternalSessionDeltaSkipsRecoveryWhileTurnIsActive(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	importer := interruptedTailImporter(stamp)
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	cancel := func() {}
	registerActiveTurn(root.ID, current.Key, cancel)
	defer unregisterActiveTurn(root.ID, current.Key)

	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 0 {
		t.Fatalf("本进程正在跑这一轮时不得当成被中断的尾巴，实际读了 %d 次", importer.calls)
	}
}

func TestSyncExternalSessionDeltaRecoversLiveTailOnlyOncePerTurn(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	// 转录里只有用户条目（这一轮确实没有回复，比如 provider 403）：不该每次轮询都重读。
	importer := &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		{Role: "user", Content: "这是测试", Timestamp: stamp.Add(time.Second)},
	}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)

	if importer.calls != 1 {
		t.Fatalf("同一轮每进程只尝试一次，实际读了 %d 次", importer.calls)
	}
	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("没有回复时不得新增行，实际 %d 行", len(latest.Exchanges))
	}
}

func TestPersistOpenedAgentSessionBindingOnlyFillsEmptyBinding(t *testing.T) {
	ctx := context.Background()
	_, manager := newLiveTailTestManager(t)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "FirstTurnWindow"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	// SendMessage 在打开运行时之前先 EnsureAgentBinding（agent 已定、转录 id 还没有）。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("前提不成立：新绑定应当是空转录 id，实际 %q", binding.AgentSessionID)
	}

	persistOpenedAgentSessionBinding(ctx, manager, current, "codex", binding, &fakeUsecaseAgentSession{id: "thread-first"})
	binding, err = manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "thread-first" {
		t.Fatalf("打开后必须立刻写回转录 id（否则首轮被重启掐断就无从 resume/补录），实际 %q", binding.AgentSessionID)
	}

	// 已有转录 id 的绑定属于「resume」，这里不许覆盖（resume 的 rebind 另有专门逻辑）。
	persistOpenedAgentSessionBinding(ctx, manager, current, "codex", binding, &fakeUsecaseAgentSession{id: "thread-other"})
	binding, err = manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "thread-first" {
		t.Fatalf("非空绑定不得被这里的补写覆盖，实际 %q", binding.AgentSessionID)
	}
}
