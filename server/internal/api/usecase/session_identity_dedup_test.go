package usecase

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// S2c：身份判重（方案 §6 / D10）。
//
// 层次：**行为**测试 —— 直接调判重谓词，断言「这一行算不算已经写过」。
//
// 为什么必须换掉纯内容判重：实测本机 `1791339665-fb5134041392` 有 9 对
// 「live 写一遍 + import 又写一遍」的重复行（`hidden=9`），全部是 dsh：
// 导入器把整段会话重放了一遍，而它的时间戳是**合成**的（`time.Now()`），
// 于是「内容 + ±5s」判重和 `TimestampFloor` 双双失效。同一个会话里还有
// import + import 的重复（两次导入相隔 20s）—— 同样漏掉。
//
// 注意：同一个会话里「请继续」「继续」「/compact」各出现 2~7 次，那些是
// **用户真的重发过**（相隔数小时），不是重复。判重**不得**把它们吞掉。
// 本文件用「相隔数小时的相同内容」把这条边界钉住。

func identityExchange(seq int, role, content, nativeTurn, nativePrompt string, ts time.Time) session.Exchange {
	return session.Exchange{
		Seq:          seq,
		Role:         role,
		Content:      content,
		NativeTurn:   nativeTurn,
		NativePrompt: nativePrompt,
		Timestamp:    ts,
	}
}

func identityTarget(exchanges ...session.Exchange) *session.Session {
	return &session.Session{Key: "k", Exchanges: exchanges}
}

// ── ① 身份相等 = 铁证，内容不同也算重复 ──────────────────────────────────────
//
// 为什么内容会不同：导入器会把**相邻同角色**条目合并成一条（claude:1275 / acp:238），
// 所以同一条 agent 消息在库里可能是「分两行」，在转录里是「一行」。
// 纯内容判重对这种形态永远漏，而 native id 一眼就对上了。
func TestIdentityDedupMatchesByNativeIDEvenWhenContentDiffers(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "agent", "前半段", "msg-uuid-1", "", ts),
	)
	if !exchangeAlreadyRecorded(target, "agent", "msg-uuid-1", "前半段\n\n后半段", ts.Add(3*time.Hour)) {
		t.Fatal("native id 相等就是同一条消息，内容不同（导入器合并过）也必须判为已写过")
	}
}

// user 行看 native_prompt，agent 行看 native_turn —— 两把键不能串。
func TestIdentityDedupKeepsRoleKeysSeparate(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "agent", "回答", "shared-id", "", ts),
	)
	if exchangeAlreadyRecorded(target, "user", "shared-id", "提问", ts) {
		t.Fatal("agent 行的 native_turn 不得当成 user 行的 native_prompt")
	}
	target2 := identityTarget(
		identityExchange(12, "user", "提问", "", "shared-id", ts),
	)
	if exchangeAlreadyRecorded(target2, "agent", "shared-id", "回答", ts) {
		t.Fatal("user 行的 native_prompt 不得当成 agent 行的 native_turn")
	}
}

// ── ② native id 对不上 = 不是重复，放行 ──────────────────────────────────────
func TestIdentityDedupPassesRowsWithUnmatchedNativeID(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "agent", "同一段内容", "msg-uuid-1", "", ts),
	)
	if exchangeAlreadyRecorded(target, "agent", "msg-uuid-2", "同一段内容", ts) {
		t.Fatal("native id 不同就是两条不同的消息，哪怕内容一模一样（用户真的重发过）")
	}
}

// ── ③ 没有 native id 才退回内容 + ±5s ────────────────────────────────────────
func TestIdentityDedupFallsBackToContentWithinTolerance(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "user", "请继续", "", "", ts),
	)
	if !exchangeAlreadyRecorded(target, "user", "", "请继续", ts.Add(2*time.Second)) {
		t.Fatal("没有 native id 时必须退回内容判重，否则历史数据的双写永远折叠不掉")
	}
	if exchangeAlreadyRecorded(target, "user", "", "请继续", ts.Add(3*time.Hour)) {
		t.Fatal("相隔数小时的相同内容必须放行 —— 那是用户真的重发（实测「请继续」出现过 7 次）")
	}
	if exchangeAlreadyRecorded(target, "user", "", "别的话", ts) {
		t.Fatal("内容不同不是重复")
	}
}

// 有 native id 时**不得**再退回内容判重：否则「同一条 agent 消息分两行落库、
// 但 native id 已经被清掉（repoint）」这类行会被内容误判吞掉。
func TestIdentityDedupDoesNotFallBackWhenNativeIDPresent(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "agent", "同一段内容", "", "", ts),
	)
	if exchangeAlreadyRecorded(target, "agent", "msg-uuid-9", "同一段内容", ts) {
		t.Fatal("带了 native id 就不许猜 —— D10：对不上就放行 + 记日志，永不按内容猜")
	}
}

// 空内容行（只调工具、没有正文的助手轮）不参与内容判重 —— 它是占位，不是同一句话。
// 但**带 native id 时仍然按身份判重**：那才是它的真身份。
func TestIdentityDedupIgnoresEmptyContentButHonorsIdentity(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	target := identityTarget(
		identityExchange(12, "agent", "", "", "", ts),
		identityExchange(13, "agent", "", "msg-uuid-5", "", ts),
	)
	if exchangeAlreadyRecorded(target, "agent", "", "", ts) {
		t.Fatal("空内容行不是同一句话，不能按内容折叠")
	}
	if !exchangeAlreadyRecorded(target, "agent", "msg-uuid-5", "", ts) {
		t.Fatal("空内容行照样有 native id，身份相等就是重复")
	}
}

// 零时间戳 + 无 native id：无法证明任何事，一律放行。
func TestIdentityDedupPassesWhenNothingToProve(t *testing.T) {
	target := identityTarget(identityExchange(12, "user", "内容", "", "", time.Time{}))
	if exchangeAlreadyRecorded(target, "user", "", "内容", time.Time{}) {
		t.Fatal("时间戳为零且没有 native id 时无法证明，必须放行")
	}
}

// ── 端到端：Full 同步的整段重放必须是幂等的 ─────────────────────────────────
//
// 这是 S2c 要根治的真实故障。ACP 协议只有 `LoadSession`，没有字节游标、没有增量
// 锚点，所以**每一次 Full 同步都是整段重放**。在 S2c 之前它靠「内容 + ±5s」判重，
// 而同时踩中三个坑：
//   · 所有权守卫让 live-owned 会话**只做 Full**，重放的是全部历史；
//   · 导入器把相邻同角色条目合并 → Content 与实时路径写的行**逐字不同**；
//   · ACP 的 `SessionUpdate` 不带时间戳 → 导入行的 Timestamp 是 `time.Now()` 伪造的，
//     实时行的 Timestamp 是那一轮真实发生的时间，两者能差几小时。
// 于是两道防线同时失效。实测 2026-10-07：本机会话 `1791339665-fb5134041392`
// Full 同步后多出 9 对 live/import 重复行，另有 3 对 import+import（相隔 20s）。
//
// 这里把「重放一次 = 库里不变」钉成不变量，走的是真正的 SyncExternalSessionDelta。

const (
	replayPromptID = "550e8400-e29b-41d4-a716-446655440000"
	replayTurnID   = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// newLiveOwnedSyncSession 建一个 live-owned 的 dsh 会话：实时路径已经写过一轮
// 助手回答（只拿到流式快照的前半段，带 native_turn），转录里是合并后的全文。
func newLiveOwnedSyncSession(t *testing.T, ctx context.Context) (rootID string, manager *session.Manager, svc *Service, current *session.Session, key string) {
	t.Helper()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	mgr := session.NewManager(root)
	created, err := mgr.Create(ctx, session.CreateInput{
		Type: session.TypeChat, Agent: "dsh", Name: "Live",
	})
	if err != nil {
		t.Fatal(err)
	}
	liveCtx := session.WithExchangeIdentity(
		session.WithExchangeSource(ctx, session.ExchangeSourceLive),
		session.ExchangeIdentity{
			TurnID:       replayPromptID,
			TurnSeq:      1,
			NativePrompt: replayPromptID,
			NativeTurn:   replayTurnID,
		})
	// 时间戳是**那一轮真实发生的时间**（两小时前），而导入器给的是 now ——
	// 两者差两小时，内容+±5s 的判重必然判不出来。这就是实测里的那 9 对。
	if err := mgr.AddExchangeForAgentAt(liveCtx, created, "user", "请继续", "dsh", "", "", "", time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddExchangeForAgentAt(liveCtx, created, "agent", "前半段", "dsh", "", "", "", time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	live, err := mgr.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateAgentState(ctx, live, "dsh", 2, "agent-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		{Role: "user", Content: "请继续", NativeID: replayPromptID},
		{Role: "agent", Content: "前半段\n\n后半段", NativeID: replayTurnID},
	}}
	return root.ID, mgr, &Service{Registry: &syncDeltaTestRegistry{root: root, manager: mgr, importer: importer}}, live, created.Key
}

func TestFullSyncReplayAddsNoRowsToLiveOwnedSession(t *testing.T) {
	ctx := context.Background()
	rootID, manager, svc, current, key := newLiveOwnedSyncSession(t, ctx)
	before := len(current.Exchanges)

	out, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
		RootID: rootID, Key: key, Full: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Exchanges) != before {
		t.Fatalf("Full 同步后 %d 行，want %d —— 整段重放没有被身份判重拦住：%+v",
			len(after.Exchanges), before, after.Exchanges)
	}
	if out.ImportedCount != 0 {
		t.Fatalf("ImportedCount = %d, want 0（重放的每一行都已在库里）", out.ImportedCount)
	}
}

// 连续两次 Full 同步：第二次不能因为「第一次写了行」而又判不出来。
func TestRepeatedFullSyncStaysIdempotent(t *testing.T) {
	ctx := context.Background()
	rootID, manager, svc, current, key := newLiveOwnedSyncSession(t, ctx)
	for i := 0; i < 3; i++ {
		if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
			RootID: rootID, Key: key, Full: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Exchanges) != len(current.Exchanges) {
		t.Fatalf("三次同步后 %d 行，want %d", len(after.Exchanges), len(current.Exchanges))
	}
}

// 身份**没匹配上**的行必须落库 —— 那正是「MindFS 宕机期间 agent 自己跑了一轮」。
// 不写进去等于丢内容，而丢内容正是用户最初报的 bug。
func TestFullSyncPersistsImportedRowWithUnmatchedNativeID(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "dsh", Name: "Live"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, created, "user", "你好", "dsh", "", "", "", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	// ctx_seq=0：转录里还有没导入过的内容（MindFS 宕机期间 agent 自己跑的那一轮）。
	if err := manager.UpdateAgentState(ctx, current, "dsh", 0, "agent-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
		{Role: "agent", Content: "宕机期间写的回答", NativeID: "7c9e6679-7425-40de-944b-e07fc1f90ae7"},
	}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
		RootID: root.ID, Key: created.Key, Full: true,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Exchanges) != 2 {
		t.Fatalf("exchanges = %d, want 2（身份对不上不等于重复，必须写进去）", len(after.Exchanges))
	}
	got := after.Exchanges[1]
	if got.NativeTurn != "7c9e6679-7425-40de-944b-e07fc1f90ae7" {
		t.Fatalf("native_turn = %q，导入行必须记住自己的身份，否则下次同步认不出自己", got.NativeTurn)
	}
}
