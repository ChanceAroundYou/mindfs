package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// 这一组钉的是「重启丢记录、同步补不回来」的两个根因：
//
//  1. 手点「同步」（Full）以前对 live-owned 会话走通用导入，时间地板取「最后一行时间戳」
//     —— 中断时最后一行是回合收尾才写的空占位行，比转录里的回复还晚，地板把要补的内容
//     全挡掉；而通用导入不复用尾部占位行，还会追加重复用户行。修复后 Full 也会先走
//     recoverInterruptedLiveTurn（用户行时刻当起点 + 强制重读 + 复用占位行），再继续通用
//     导入补子代理等尾部之外的内容。
//  2. 自动增量对「转录还没 flush」的尾巴每进程只尝试一次；用户手点同步必须能重试。

// floorAwareTailImporter 像真导入器一样按 AfterTimestamp / TimestampFloor 过滤并有条数记录，
// 不像 syncDeltaTestImporter 那样无视地板 —— 因为这里要证明的正是「地板选错会吃掉回复」。
type floorAwareTailImporter struct {
	exchanges []agenttypes.ImportedExchange
	inputs    []agenttypes.ImportExternalSessionInput
	calls     int
}

func (i *floorAwareTailImporter) AgentName() string               { return "codex" }
func (i *floorAwareTailImporter) HasTranscriptTimestamps() bool    { return true }
func (i *floorAwareTailImporter) ListExternalSessions(context.Context, agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	return agenttypes.ListExternalSessionsResult{}, nil
}
func (i *floorAwareTailImporter) ImportExternalSession(_ context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	i.calls++
	i.inputs = append(i.inputs, in)
	out := make([]agenttypes.ImportedExchange, 0, len(i.exchanges))
	for _, ex := range i.exchanges {
		if !in.AfterTimestamp.IsZero() && !ex.Timestamp.After(in.AfterTimestamp) {
			continue
		}
		if !in.TimestampFloor.IsZero() && !ex.Timestamp.After(in.TimestampFloor) {
			continue
		}
		out = append(out, ex)
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.AgentName(),
		AgentSessionID: in.AgentSessionID,
		Cwd:            in.RootPath,
		Exchanges:      out,
	}, nil
}

func (i *floorAwareTailImporter) sawRecoveryCall(stamp time.Time) bool {
	for _, in := range i.inputs {
		if in.AfterTimestamp.Equal(stamp) && in.ForceRead {
			return true
		}
	}
	return false
}

// Full 同步必须补回被掐断的尾巴：用用户行时刻当起点、强制重读、复用尾部空占位行，
// 且后续通用导入按「地板」把尾巴那段滤掉、不产生重复用户行。
func TestSyncFullSyncRecoversInterruptedLiveTail(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	importer := &floorAwareTailImporter{}
	importer.exchanges = interruptedTailImporter(stamp).exchanges
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{
		RootID: root.ID, Key: current.Key, Full: true,
	}); err != nil {
		t.Fatal(err)
	}

	if !importer.sawRecoveryCall(stamp) {
		t.Fatalf("Full 同步必须以用户行时刻 %v 为起点强制重读补回尾巴，实际调用：%+v", stamp, importer.inputs)
	}
	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("必须复用尾部空助手行、不新增重复用户行，实际 %d 行", len(latest.Exchanges))
	}
	if latest.Exchanges[0].Role != "user" {
		t.Fatalf("首行角色被破坏: %q", latest.Exchanges[0].Role)
	}
	if text := blocksText(blocksForSeq(t, manager, current.Key, 2)); !strings.Contains(text, "using-superpowers") {
		t.Fatalf("恢复的正文没挂到助手行上: %q", text)
	}
}

// 自动增量首拍转录还没 flush（读到 0 条、每进程只试一次）；用户手点「同步」必须能重试补回。
func TestSyncFullSyncRetriesInterruptedTailAfterEmptyFirstAttempt(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedInterruptedLiveTurn(t, manager)
	importer := &floorAwareTailImporter{} // 第一拍：转录还没有可读内容
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key) // 增量：0 条，记下「这一轮试过了」

	importer.exchanges = interruptedTailImporter(stamp).exchanges // 转录已 flush
	if _, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{
		RootID: root.ID, Key: current.Key, Full: true,
	}); err != nil {
		t.Fatal(err)
	}

	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("手点同步应补回尾巴、复用占位行，实际 %d 行", len(latest.Exchanges))
	}
	if text := blocksText(blocksForSeq(t, manager, current.Key, 2)); !strings.Contains(text, "using-superpowers") {
		t.Fatalf("首拍读空后手点同步没补回内容: %q", text)
	}
}

// persistLearnedAgentSessionID：id 一出现就补进空绑定，非空绑定绝不覆盖，空 id 不作数。
func TestPersistLearnedAgentSessionIDFillsEmptyBindingOnly(t *testing.T) {
	ctx := context.Background()
	_, manager := newLiveTailTestManager(t)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "ClaudeFirstTurn"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureAgentBinding(ctx, created.Key, "claude"); err != nil {
		t.Fatal(err)
	}

	// id 还没出现（claude 打开那一刻 SessionID() 为空）：不作数、下一条消息再试。
	if done := persistLearnedAgentSessionID(ctx, manager, current, "claude", &fakeUsecaseAgentSession{id: ""}); done {
		t.Fatal("空 id 不该被当成已确定")
	}
	binding, err := manager.FindAgentBinding(ctx, created.Key, "claude")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("空 id 不该写入绑定，实际 %q", binding.AgentSessionID)
	}

	// 第一条流消息把 CLI session id 带上来：必须立刻写进空绑定。
	if done := persistLearnedAgentSessionID(ctx, manager, current, "claude", &fakeUsecaseAgentSession{id: "cli-abc"}); !done {
		t.Fatal("拿到 id 后应当标为已确定")
	}
	binding, err = manager.FindAgentBinding(ctx, created.Key, "claude")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "cli-abc" {
		t.Fatalf("回合进行中必须把转录 id 落库（否则首轮重启就断线），实际 %q", binding.AgentSessionID)
	}

	// 非空绑定属于 resume/已写，绝不被覆盖。
	persistLearnedAgentSessionID(ctx, manager, current, "claude", &fakeUsecaseAgentSession{id: "cli-other"})
	binding, _ = manager.FindAgentBinding(ctx, created.Key, "claude")
	if binding.AgentSessionID != "cli-abc" {
		t.Fatalf("非空绑定不得被覆盖，实际 %q", binding.AgentSessionID)
	}
}

// 首轮中途被重启的孤儿会话：绑定里没有 id，但转录还在盘上。按「首条用户消息 + cwd」
// 认领唯一匹配的转录，补回绑定并补回内容。
func seedOrphanedLiveTurn(t *testing.T, manager *session.Manager, userText string) (*session.Session, time.Time) {
	t.Helper()
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Orphaned"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
	if err := manager.EnsureAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistUserTurnExchange(ctx, manager, current, userText, "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	current, err = manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	return current, stamp
}

func TestSyncAdoptsOrphanedLiveTailWhenBindingEmpty(t *testing.T) {
	const userText = "这是用于认领孤立转录的第一条用户消息"
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedOrphanedLiveTurn(t, manager, userText)
	importer := &syncDeltaTestImporter{
		external: []agenttypes.ExternalSessionSummary{{
			Agent:          "codex",
			AgentSessionID: "thread-orphan",
			Cwd:            root.RootPath,
			FirstUserText:  userText + "\n\nMindFS context: root_id=root",
		}},
		exchanges: interruptedTailImporter(stamp).exchanges,
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)

	binding, err := manager.FindAgentBinding(context.Background(), current.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "thread-orphan" {
		t.Fatalf("应当认领唯一匹配的转录，实际绑定 %q", binding.AgentSessionID)
	}
	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("认领后应补出一条助手行，实际 %d 行", len(latest.Exchanges))
	}
	if text := blocksText(blocksForSeq(t, manager, current.Key, latest.Exchanges[1].Seq)); !strings.Contains(text, "using-superpowers") {
		t.Fatalf("认领后没补回正文: %q", text)
	}
}

// 唯一候选但首条消息对不上（别的会话）：绝不认领、不导入。
func TestSyncDoesNotAdoptUnrelatedTranscript(t *testing.T) {
	const userText = "这是用于认领孤立转录的第一条用户消息"
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedOrphanedLiveTurn(t, manager, userText)
	importer := &syncDeltaTestImporter{
		external: []agenttypes.ExternalSessionSummary{{
			Agent:          "codex",
			AgentSessionID: "thread-unrelated",
			Cwd:            root.RootPath,
			FirstUserText:  "完全不相干的另一段开场白，长度足够但不是这条会话",
		}},
		exchanges: interruptedTailImporter(stamp).exchanges,
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)

	binding, err := manager.FindAgentBinding(context.Background(), current.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("首条消息对不上时不得认领，实际绑定 %q", binding.AgentSessionID)
	}
	if importer.calls != 0 {
		t.Fatalf("未认领就不该导入，实际读了 %d 次", importer.calls)
	}
}

func TestSyncDoesNotAdoptAmbiguousTranscripts(t *testing.T) {
	const userText = "这是用于认领孤立转录的第一条用户消息"
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedOrphanedLiveTurn(t, manager, userText)
	candidate := agenttypes.ExternalSessionSummary{
		Agent:         "codex",
		Cwd:           root.RootPath,
		FirstUserText: userText + "\n\nMindFS context: ...",
	}
	first := candidate
	first.AgentSessionID = "thread-a"
	second := candidate
	second.AgentSessionID = "thread-b"
	importer := &syncDeltaTestImporter{
		external:  []agenttypes.ExternalSessionSummary{first, second},
		exchanges: interruptedTailImporter(stamp).exchanges,
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	runLiveTailSync(t, svc, root.ID, current.Key)

	binding, err := manager.FindAgentBinding(context.Background(), current.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("binding: %v %v", binding, err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("有歧义时不得认领，实际绑定 %q", binding.AgentSessionID)
	}
	if importer.calls != 0 {
		t.Fatalf("认领放弃后不该导入，实际读了 %d 次", importer.calls)
	}
	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 1 {
		t.Fatalf("有歧义时不得新增行，实际 %d 行", len(latest.Exchanges))
	}
}
