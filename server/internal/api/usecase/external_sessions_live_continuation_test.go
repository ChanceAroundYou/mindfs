package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// continuationTailImporter 在 syncDeltaTestImporter 之上补两件事：可以返回字节游标
// （续写补录要落它），以及按次数换一组结果（模拟「续写还在跑 → 收尾」的推进）。
type continuationTailImporter struct {
	syncDeltaTestImporter
	cursor  agenttypes.ExternalSessionCursor
	batches [][]agenttypes.ImportedExchange
}

func (i *continuationTailImporter) ImportExternalSession(ctx context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	i.input = in
	i.calls++
	exchanges := i.exchanges
	if len(i.batches) > 0 {
		exchanges = i.batches[0]
		i.batches = i.batches[1:]
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.AgentName(),
		AgentSessionID: in.AgentSessionID,
		Cwd:            in.RootPath,
		Exchanges:      exchanges,
		Cursor:         i.cursor,
	}, nil
}

// seedSettledLiveTurn 造出「一轮已经正常收尾」的形状：用户行 + 带块的助手行。
// 续写补录只在这种形状上工作（尾部是用户行 = 这一轮没走完，归中断回合补录管）。
func seedSettledLiveTurn(t *testing.T, manager *session.Manager) (*session.Session, time.Time) {
	t.Helper()
	current, stamp := seedInterruptedLiveTurn(t, manager)
	if err := persistTurnBlocks(context.Background(), manager, current.Key, current.Exchanges[len(current.Exchanges)-1].Seq, "turn-u4", "已落库的回复", nil); err != nil {
		t.Fatal(err)
	}
	latest, err := manager.Get(context.Background(), current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	return latest, stamp
}

func exchangeTexts(exchanges []session.Exchange) []string {
	out := make([]string, 0, len(exchanges))
	for _, exchange := range exchanges {
		out = append(out, exchange.Role+":"+strings.TrimSpace(exchange.LegacyContent))
	}
	return out
}

// 复现（2026-11-22，llm-deploy 会话）：回合在 06:33:11 收尾落盘，CLI 随后因后台任务
// 回注自己又跑到 06:39:27。那一段实时可见却没人落盘 —— 这里钉住「下次同步把它补成
// 助手行」，并且只补助手侧（用户行由实时路径在发送时就写好了）。
func TestSyncExternalSessionDeltaRecoversLiveContinuationTail(t *testing.T) {
	ctx := context.Background()
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedSettledLiveTurn(t, manager)
	floor := stamp.Add(44 * time.Second) // 最后一行（助手行）的落盘时刻

	importer := &continuationTailImporter{
		syncDeltaTestImporter: syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
			// 导入器会把被过滤掉的后台通知前后的助手条目并成一条；用户行不该被补第二遍。
			{Role: "user", Content: "后台任务完成通知", Timestamp: floor.Add(2 * time.Minute), NativeID: "n-cont"},
			{Role: "agent", Content: "续写一\n\n续写二", Timestamp: floor.Add(6 * time.Minute), NativeID: "a-cont"},
		}},
		cursor: agenttypes.ExternalSessionCursor{SourcePath: "/tmp/transcript.jsonl", Offset: 900, CommittedOffset: 900},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	// 没有「回合结束之后还有事件」的标记：这条路径一次都不该读转录。
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 0 {
		t.Fatalf("没有续写标记时不得读转录，实际读了 %d 次", importer.calls)
	}

	markSessionContinuationTail(root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 1 {
		t.Fatalf("标记之后应读一次转录，实际 %d 次", importer.calls)
	}
	if importer.input.AgentSessionID != "thread-1" {
		t.Fatalf("应按绑定里的转录 id 导入，得到 %q", importer.input.AgentSessionID)
	}
	if !importer.input.TimestampFloor.Equal(floor) {
		t.Fatalf("时间地板应取最后一行的落盘时刻 %s，得到 %s", floor, importer.input.TimestampFloor)
	}

	latest, err := manager.Get(ctx, current.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 3 {
		t.Fatalf("应补出且只补出一条助手行（用户行不重复），实际 %d 行: %v", len(latest.Exchanges), exchangeTexts(latest.Exchanges))
	}
	last := latest.Exchanges[2]
	if last.Role != "agent" {
		t.Fatalf("续写应落成助手行，得到 %q", last.Role)
	}
	text := blocksText(blocksForSeq(t, manager, current.Key, last.Seq))
	if !strings.Contains(text, "续写一") || !strings.Contains(text, "续写二") {
		t.Fatalf("续写正文没落盘: %q", text)
	}
	if strings.TrimSpace(last.MindFSID) == "" {
		t.Fatalf("补出的行必须带 mindfs_id（前端靠它去重瞬时行）")
	}

	// 字节游标要推进：这条路径下次才能只 stat 不重读。
	binding, err := manager.FindAgentBinding(ctx, current.Key, "codex")
	if err != nil || binding == nil {
		t.Fatalf("绑定应仍存在: %v", err)
	}
	if cursor := binding.ExternalCursor(); cursor.Offset != 900 || cursor.CommittedOffset != 900 {
		t.Fatalf("游标应推进到导入器返回的位置，得到 %+v", cursor)
	}

	// 标记取走即清：再同步一次不读转录。
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 1 {
		t.Fatalf("标记已消费，不该再读转录，实际 %d 次", importer.calls)
	}
}

// 续写可能分几次才收尾：导出器判定尾部还没走完时（committed < offset）不提交，
// 标记要放回去让下一拍再试；收尾后落一次；同一条转录 native id 判重，重放不再多行。
func TestSyncExternalSessionDeltaLiveContinuationTailRetriesUntilClosedAndStaysIdempotent(t *testing.T) {
	ctx := context.Background()
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedSettledLiveTurn(t, manager)

	continuation := agenttypes.ImportedExchange{
		Role: "agent", Content: "续写", Timestamp: stamp.Add(5 * time.Minute), NativeID: "a-cont",
	}
	importer := &continuationTailImporter{
		cursor: agenttypes.ExternalSessionCursor{SourcePath: "/tmp/transcript.jsonl", Offset: 900, CommittedOffset: 600},
		batches: [][]agenttypes.ImportedExchange{
			// 第一拍：续写还在跑，导入器把没走完的尾巴扣下了。
			nil,
			// 第二拍：收尾，交出续写。
			{continuation},
			// 之后的重放（文件又长了）拿到的还是同一条 native id。
			{continuation},
		},
	}
	importer.exchanges = []agenttypes.ImportedExchange{continuation}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	markSessionContinuationTail(root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 1 {
		t.Fatalf("应读一次转录，实际 %d 次", importer.calls)
	}
	latest, _ := manager.Get(ctx, current.Key, 0)
	if len(latest.Exchanges) != 2 {
		t.Fatalf("尾巴没走完时不得补行，实际 %d 行", len(latest.Exchanges))
	}

	// 标记被放回去了：下一拍（没有新事件）仍会再试。
	importer.cursor = agenttypes.ExternalSessionCursor{SourcePath: "/tmp/transcript.jsonl", Offset: 1200, CommittedOffset: 1200}
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 2 {
		t.Fatalf("尾巴未收尾时标记应保留，实际读了 %d 次", importer.calls)
	}
	latest, _ = manager.Get(ctx, current.Key, 0)
	if len(latest.Exchanges) != 3 {
		t.Fatalf("收尾后应补一条助手行，实际 %d 行: %v", len(latest.Exchanges), exchangeTexts(latest.Exchanges))
	}

	// 收尾之后标记已经清掉：不再读。
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 2 {
		t.Fatalf("收尾后不该再读转录，实际 %d 次", importer.calls)
	}

	// 重放同一条 native id：不新增行（判重与发送侧同源）。
	markSessionContinuationTail(root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 3 {
		t.Fatalf("应再读一次转录验证判重，实际 %d 次", importer.calls)
	}
	latest, _ = manager.Get(ctx, current.Key, 0)
	if len(latest.Exchanges) != 3 {
		t.Fatalf("同一条续写不该落第二行，实际 %d 行: %v", len(latest.Exchanges), exchangeTexts(latest.Exchanges))
	}
}

// 回合还在跑时不补：那一轮由实时路径负责，导入器插进来就是双写。
func TestSyncExternalSessionDeltaSkipsContinuationTailWhileTurnActive(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	current, stamp := seedSettledLiveTurn(t, manager)
	importer := &continuationTailImporter{
		syncDeltaTestImporter: syncDeltaTestImporter{exchanges: []agenttypes.ImportedExchange{
			{Role: "agent", Content: "续写", Timestamp: stamp.Add(5 * time.Minute), NativeID: "a-cont"},
		}},
		cursor: agenttypes.ExternalSessionCursor{SourcePath: "/tmp/transcript.jsonl", Offset: 900, CommittedOffset: 900},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	registerActiveTurn(root.ID, current.Key, func() {})
	defer unregisterActiveTurn(root.ID, current.Key)
	markSessionContinuationTail(root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 0 {
		t.Fatalf("回合进行中不得读转录，实际 %d 次", importer.calls)
	}
	// 标记不能丢：回合结束后下一拍还要补。
	unregisterActiveTurn(root.ID, current.Key)
	runLiveTailSync(t, svc, root.ID, current.Key)
	if importer.calls != 1 {
		t.Fatalf("回合结束后应补一次，实际 %d 次", importer.calls)
	}
	latest, _ := manager.Get(context.Background(), current.Key, 0)
	if len(latest.Exchanges) != 3 {
		t.Fatalf("回合结束后应补一条助手行，实际 %d 行", len(latest.Exchanges))
	}
}
