package usecase

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 复现 2026-09-28 的 bug：agent 执行中，用户自己那条消息从对话列表消失、点同步也刷不回来。
//
// 成因是 user 行落盘太晚：回合进行中读接口拿不到它（窗口 maxSeq 只数已落盘行），
// 而广播出去的 seq 已经是「预测值」，客户端据此认定该行持久化 → overlay 的
// 「seq<=latestSeq 即让位给窗口」规则在重锚定时把它丢掉。
//
// 契约：user 行必须在**回合开始前**落盘，seq 从预测变成事实。
func TestPersistUserTurnExchangeLandsBeforeTurnAndAllocatesRealSeq(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// 先铺一轮历史，让 seq 不从 1 起算。
	if err := manager.AddExchangeForAgentAt(ctx, current, "user", "first", "claude", "", "", "", time.Now().UTC()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := manager.AddExchangeForAgent(ctx, current, "agent", "reply", "claude", "", "", ""); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	current, err = manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}
	before := len(current.Exchanges)
	wantSeq := session.MaxExchangeSeq(current.Exchanges) + 1

	// 这一步就是 SendMessage 起手做的事（此刻 agent 还没开始跑、助手行还不存在）。
	ts := time.Now().UTC()
	gotSeq, err := persistUserTurnExchange(ctx, manager, current, "本轮提问", "claude", "", "", "", ts)
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if gotSeq != wantSeq {
		t.Fatalf("user seq = %d, want %d（必须是真实分配值，不是 max+1 的预测）", gotSeq, wantSeq)
	}
	if len(current.Exchanges) != before+1 {
		t.Fatalf("exchanges len = %d, want %d：写入后 current 指针必须被就地追加", len(current.Exchanges), before+1)
	}

	// 关键断言：会话元数据里已经能看到这一行（而不是只存在于内存 pending 态）。
	reloaded, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var found *session.Exchange
	for i := range reloaded.Exchanges {
		if reloaded.Exchanges[i].Role == "user" && reloaded.Exchanges[i].Content == "本轮提问" {
			found = &reloaded.Exchanges[i]
		}
	}
	if found == nil {
		t.Fatalf("回合进行中读不到本轮 user 行：%#v", reloaded.Exchanges)
	}
	if found.Seq != wantSeq {
		t.Fatalf("落盘 seq = %d, want %d", found.Seq, wantSeq)
	}
	if !found.Timestamp.Equal(ts) {
		t.Fatalf("落盘时间戳 = %s, want %s", found.Timestamp, ts)
	}

	// 窗口 maxSeq 必须已覆盖本轮 user 行——前端据此才不会把该行判成「窗口外」丢弃。
	_, meta, err := manager.GetWindow(ctx, key, 0, 50, 20)
	if err != nil {
		t.Fatalf("GetWindow: %v", err)
	}
	if meta == nil || meta.MaxSeq < wantSeq {
		t.Fatalf("window maxSeq = %+v，必须 >= 本轮 user seq %d", meta, wantSeq)
	}
}

// 同一轮的 user 行有两个写入者：外部转录导入器（external_sessions.go）与发送侧。
// 判据不对称就会落两条（2026-09-13 实测 seq=57/58）。前移到回合开始后仍必须对称。
func TestPersistUserTurnExchangeSkipsWhenImporterAlreadyWrote(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ts := time.Now().UTC()
	// 模拟导入器抢先写入（同内容、同时间窗）。
	if err := manager.AddExchangeForAgentAt(session.WithExchangeSource(ctx, session.ExchangeSourceImport), current, "user", "本轮提问", "claude", "", "", "", ts); err != nil {
		t.Fatalf("importer write: %v", err)
	}
	current, err = manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}
	before := len(current.Exchanges)

	gotSeq, err := persistUserTurnExchange(ctx, manager, current, "本轮提问", "claude", "", "", "", ts)
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if gotSeq == 0 {
		t.Fatalf("命中判重时必须回传已有那条的 seq，不能返回 0")
	}
	if len(current.Exchanges) != before {
		t.Fatalf("命中判重不得再写一行：len %d -> %d", before, len(current.Exchanges))
	}
}

// 真实重发（「继续」）不能被误判成重复：容忍窗只有 5s（userExchangeRepeatTolerance）。
func TestPersistUserTurnExchangeKeepsGenuineResendOutsideWindow(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	base := time.Now().UTC()
	if err := manager.AddExchangeForAgentAt(ctx, current, "user", "继续", "claude", "", "", "", base); err != nil {
		t.Fatalf("first: %v", err)
	}
	current, err = manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}
	firstSeq := session.MaxExchangeSeq(current.Exchanges)

	// 30s 后的真实重发必须落成新行。
	gotSeq, err := persistUserTurnExchange(ctx, manager, current, "继续", "claude", "", "", "", base.Add(30*time.Second))
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if gotSeq <= firstSeq {
		t.Fatalf("窗外真实重发必须拿到更大的 seq：got %d, first %d", gotSeq, firstSeq)
	}
}

// 端到端锁死契约：回合**执行中**（命令还在跑、助手行尚未落盘）读会话，
// 本轮 user 行必须已经带着真实 seq 可见——这正是 2026-09-28 bug 的时间窗。
//
// 旧实现在这里读到的是「什么都读不到」：user 行要等回合结束才写，而窗口 maxSeq
// 只数已落盘行。客户端却已按广播出去的预测 seq 认定该行持久化，于是 overlay 在
// 重锚定时把它丢掉，表现为 agent 执行中自己的消息从列表消失。
func TestUserRowVisibleDuringLiveTurn(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeCommand,
		Name: "Command",
	})
	if err != nil {
		t.Fatalf("create command session: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var startSeq int
	var checkedDuringTurn bool
	err = service.SendMessage(ctx, SendMessageInput{
		RootID:  root.ID,
		Key:     created.Key,
		Content: "sleep 10",
		OnStart: func(start MessageStart) {
			// 广播出去的 seq 必须是已落盘的真值，而不是 max+1 的预测。
			startSeq = start.UserExchangeSeq
			if startSeq <= 0 {
				t.Errorf("UserExchangeSeq = %d, want a real persisted seq", startSeq)
			}
		},
		OnUpdate: func(event agenttypes.Event) {
			toolCall, ok := event.Data.(agenttypes.ToolCall)
			if !ok || toolCall.Meta["source"] != "userShell" || toolCall.Meta["phase"] != "start" {
				return
			}
			if checkedDuringTurn {
				return
			}
			checkedDuringTurn = true
			// 此刻命令正在跑，助手行还没落盘。
			mid, err := manager.Get(context.Background(), created.Key, 0)
			if err != nil {
				t.Errorf("mid-turn Get: %v", err)
				return
			}
			var userSeq int
			for _, ex := range mid.Exchanges {
				if ex.Role == "user" && ex.Content == "sleep 10" {
					userSeq = ex.Seq
				}
			}
			if userSeq == 0 {
				t.Errorf("回合执行中读不到本轮 user 行：%#v", mid.Exchanges)
				return
			}
			if userSeq != startSeq {
				t.Errorf("落盘 seq = %d，广播 seq = %d，必须一致（广播必须是真值）", userSeq, startSeq)
			}
			// 窗口 maxSeq 必须已覆盖它——前端 overlay 靠这个判定「窗口已含」。
			_, meta, err := manager.GetWindow(context.Background(), created.Key, 0, 50, 20)
			if err != nil {
				t.Errorf("mid-turn GetWindow: %v", err)
				return
			}
			if meta == nil || meta.MaxSeq < userSeq {
				t.Errorf("window maxSeq = %+v, must cover user seq %d", meta, userSeq)
			}
			cancel()
		},
	})
	if !checkedDuringTurn {
		t.Fatalf("never observed the start event; err=%v", err)
	}
	// user 行只许写一次：命令会话的 persistCommandTurn 不应再补一条。
	final, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	userRows := 0
	for _, ex := range final.Exchanges {
		if ex.Role == "user" && ex.Content == "sleep 10" {
			userRows++
		}
	}
	if userRows != 1 {
		t.Fatalf("user 行必须恰好一条，实际 %d：%#v", userRows, final.Exchanges)
	}
}

func newEarlyUserPersistManager(t *testing.T) (*session.Manager, string) {
	t.Helper()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir()))
	s, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "EarlyPersist"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return manager, s.Key
}
