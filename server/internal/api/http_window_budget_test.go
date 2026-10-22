package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"mindfs/server/internal/session"
)

// 窗口块数预算（CUSTOM(G-JS)）的行为契约。
//
// 起因：窗口 API 的 latest/limit 数的是**轮**，而一轮的块数是 agent 行为决定的 ——
// 实测某会话单轮 3,224 块，`latest=8` 回 1,282 块 / 2.49 MB，上翻一页 4,526 块 / 9.36 MB。
// 前端一帧要挂 4,500 个时间线条目，主线程长任务 1.4–1.9 s。
//
// 这一组测试钉住四件事：
//  1. 预算按**块数**收口，不是按轮数；
//  2. minSeq/maxSeq 按**裁剪后**的轮次重算（否则客户端 loadMore 会跳过被裁的轮次）；
//  3. 被裁掉轮次的块与 aux 不出现在回包里（否则客户端合并后留下孤立块）；
//  4. 单轮自己就超预算时仍下发这一轮（空窗口比慢更糟），并标 truncated。

// addHeavyTurn 追加一轮：1 条正文交换 + n 个 reasoning 块，模拟「一轮几千块」的会话。
func addHeavyTurn(t *testing.T, manager *session.Manager, s *session.Session, n int) {
	t.Helper()
	ctx := context.Background()
	if err := manager.AddExchangeForAgent(ctx, s, "agent", "answer", "claude", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}
	current, err := manager.Get(ctx, s.Key, 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	seq := 0
	for _, ex := range current.Exchanges {
		if ex.Seq > seq {
			seq = ex.Seq
		}
	}
	blocks := make([]session.Block, 0, n)
	for i := 0; i < n; i++ {
		blocks = append(blocks, session.Block{
			Seq: seq,
			Pos: i,
			Type: session.BlockTypeReasoning,
			Text: fmt.Sprintf("thought %d", i),
		})
	}
	if err := manager.AppendBlocks(ctx, s.Key, blocks); err != nil {
		t.Fatalf("append blocks: %v", err)
	}
}

func decodeWindow(t *testing.T, rec *httptest.ResponseRecorder) ([]int, map[string][]session.Block, *session.SessionWindowMeta) {
	t.Helper()
	raw := rec.Body.Bytes()
	var body struct {
		Exchanges  []session.Exchange         `json:"exchanges"`
		Blocks     map[string][]session.Block `json:"blocks"`
		WindowMeta *session.SessionWindowMeta `json:"window_meta"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	seqs := make([]int, 0, len(body.Exchanges))
	for _, ex := range body.Exchanges {
		seqs = append(seqs, ex.Seq)
	}
	return seqs, body.Blocks, body.WindowMeta
}

func TestWindowBlockBudgetTrimServesNewestTurns(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Budget"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 4 轮 × 100 块 = 400 块；预算 250 ⇒ 只能装下最后 2 轮。
	for i := 0; i < 4; i++ {
		addHeavyTurn(t, manager, s, 100)
	}
	h := &HTTPHandler{AppContext: app, WindowBlockBudget: 250}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=4")
	seqs, blocks, meta := decodeWindow(t, rec)
	if meta == nil {
		t.Fatalf("窗口响应缺少 window_meta")
	}
	if !meta.Truncated {
		t.Fatalf("预算 250 装不下 400 块时必须标 truncated: %+v", meta)
	}
	if len(seqs) == 0 || len(seqs) >= 4 {
		t.Fatalf("裁剪后应只留最后几轮，实际 seqs=%v", seqs)
	}
	// minSeq/maxSeq 必须与回包里的轮次一致，否则客户端 loadMore 会跳过被裁的轮次。
	if meta.MinSeq != seqs[0] || meta.MaxSeq != seqs[len(seqs)-1] {
		t.Fatalf("meta 边界与回包不一致: meta=%d..%d seqs=%v", meta.MinSeq, meta.MaxSeq, seqs)
	}
	// 回包里不得出现被裁掉的 seq 的块（客户端按 seq 覆盖合并，会留下孤立块）。
	for key := range blocks {
		found := false
		for _, seq := range seqs {
			if fmt.Sprint(seq) == key {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("回包带着不在 exchanges 里的 seq=%s 的块", key)
		}
	}
	// 预算内：块数不超过预算（本轮 2 × 100 = 200 ≤ 250）。
	if meta.BlockCount > 250 {
		t.Fatalf("blockCount=%d 超过预算 250", meta.BlockCount)
	}
}

func TestWindowBlockBudgetKeepsSingleOversizedTurn(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "HugeTurn"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	addHeavyTurn(t, manager, s, 500) // 单轮就超预算
	h := &HTTPHandler{AppContext: app, WindowBlockBudget: 400}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=8")
	seqs, blocks, meta := decodeWindow(t, rec)
	if len(seqs) != 1 {
		t.Fatalf("单轮超预算时必须仍下发这一轮，实际 seqs=%v", seqs)
	}
	if !meta.Truncated {
		t.Fatalf("超预算的窗口必须标 truncated: %+v", meta)
	}
	if len(blocks[fmt.Sprint(seqs[0])]) != 500 {
		t.Fatalf("超预算轮的块必须完整下发（不能在轮内切块），实际 %d", len(blocks[fmt.Sprint(seqs[0])]))
	}
}

// 预算关闭（budget<=0）时必须逐字节回到今天的行为 —— 这是回滚路径。
func TestApplyWindowBlockBudgetDisabled(t *testing.T) {
	exchanges := []session.Exchange{{Seq: 1}, {Seq: 2}, {Seq: 3}}
	blocks := map[int][]session.Block{
		1: {{Seq: 1, Type: session.BlockTypeReasoning, Text: "a"}},
		2: {{Seq: 2, Type: session.BlockTypeReasoning, Text: "b"}},
		3: {{Seq: 3, Type: session.BlockTypeReasoning, Text: "c"}},
	}
	for _, budget := range []int{0, -1} {
		gotSession, gotMeta := applyWindowBlockBudget(&session.Session{Exchanges: exchanges}, &session.SessionWindowMeta{}, blocks, budget)
		if len(gotSession.Exchanges) != len(exchanges) || gotMeta.Truncated {
			t.Fatalf("budget=%d 必须原样返回，实际 %+v", budget, gotMeta)
		}
	}
}

// 400 块的默认预算下，8 轮小会话（每轮 10 块）完全不受影响。
func TestApplyWindowBlockBudgetLeavesSmallSessionsUntouched(t *testing.T) {
	exchanges := make([]session.Exchange, 0, 8)
	blocks := map[int][]session.Block{}
	for i := 1; i <= 8; i++ {
		exchanges = append(exchanges, session.Exchange{Seq: i})
		for p := 0; p < 10; p++ {
			blocks[i] = append(blocks[i], session.Block{Seq: i, Pos: p, Type: session.BlockTypeReasoning, Text: "x"})
		}
	}
	gotSession, gotMeta := applyWindowBlockBudget(
		&session.Session{Exchanges: exchanges},
		&session.SessionWindowMeta{},
		blocks,
		400,
	)
	if gotMeta.Truncated || len(gotSession.Exchanges) != 8 || gotMeta.BlockCount != 80 {
		t.Fatalf("小会话不该被裁剪，实际 %+v blockCount=%d", gotMeta, gotMeta.BlockCount)
	}
}

