package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"mindfs/server/internal/session"

	agenttypes "mindfs/server/internal/agent/types"
)

// GET 原样返回持久化行；判重在写入侧处理，读路径不能再用内容启发式隐藏真实行。
func TestHandleSessionGetKeepsDuplicateExchangesAndAuxOnOriginalSeq(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Dup"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stamp := time.Date(2026, 9, 14, 15, 39, 24, 0, time.UTC)
	liveCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	importCtx := session.WithExchangeSource(ctx, session.ExchangeSourceImport)
	if err := manager.AddExchangeForAgentAt(liveCtx, s, "user", "同一句话", "claude", "", "", "", stamp); err != nil {
		t.Fatalf("add live: %v", err)
	}
	// 转录那份晚 6.4 秒、还带中断标记 —— 写入侧 ±5s 判重正是这样漏的
	if err := manager.AddExchangeForAgentAt(importCtx, s, "user", "[Request interrupted by user]\n\n同一句话", "claude", "", "", "", stamp.Add(6400*time.Millisecond)); err != nil {
		t.Fatalf("add import: %v", err)
	}
	if err := manager.AddExchangeAux(ctx, s.Key, session.ExchangeAux{
		Seq:      2,
		ToolCall: &agenttypes.ToolCall{CallID: "call-import", Kind: agenttypes.ToolKindExecute, Status: "success"},
	}); err != nil {
		t.Fatalf("add aux: %v", err)
	}

	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Exchanges []session.Exchange               `json:"exchanges"`
		Aux       map[string][]session.ExchangeAux `json:"exchange_aux"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Exchanges) != 2 {
		t.Fatalf("exchanges = %d, want 2: %+v", len(payload.Exchanges), payload.Exchanges)
	}
	if payload.Exchanges[0].Source != session.ExchangeSourceLive {
		t.Fatalf("seq=1 必须是实时路径行: %+v", payload.Exchanges[0])
	}
	if payload.Exchanges[1].Seq != 2 || payload.Exchanges[1].Source != session.ExchangeSourceImport {
		t.Fatalf("seq=2 必须保留导入行: %+v", payload.Exchanges[1])
	}
	if len(payload.Aux["2"]) == 0 {
		t.Fatalf("导入行的 aux 必须保留在原 seq=2: %+v", payload.Aux)
	}
	if _, ok := payload.Aux["1"]; ok {
		t.Fatalf("aux 不应被重映射到 seq=1: %+v", payload.Aux)
	}
}

// S2b 的接线契约：窗口回包必须带上窗口内 seq 的**正文块**，且块不压正文。
// 少了它前端就只能继续吃 content + aux，S2b 等于没做。
func TestHandleSessionGetWindowInlinesBlocks(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Blocks"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const turnID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	for i := 1; i <= 3; i++ {
		if err := manager.AddExchangeForAgent(
			session.WithExchangeIdentity(ctx, session.ExchangeIdentity{MindFSID: turnID, TurnSeq: i}),
			s, "agent", "", "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange %d: %v", i, err)
		}
		// 每轮都只有 reasoning（本 bug 的形态）：没有 text 块，正文只在块流里。
		if err := manager.AppendBlocks(ctx, s.Key, []session.Block{{
			Seq: i, Pos: 0, Type: session.BlockTypeReasoning, Step: 1, MindFSID: turnID,
			Text: "reasoning-for-" + string(rune('a'+i-1)),
		}}); err != nil {
			t.Fatalf("AppendBlocks %d: %v", i, err)
		}
	}
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Blocks map[string][]session.Block `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if len(body.Blocks) != 2 {
		t.Fatalf("blocks 键数 = %d, want 2（窗口内两个 seq）body=%s", len(body.Blocks), rec.Body.String())
	}
	for _, seq := range []string{"2", "3"} {
		items := body.Blocks[seq]
		if len(items) != 1 || items[0].Type != session.BlockTypeReasoning {
			t.Fatalf("seq %s 的块 = %#v", seq, items)
		}
		if items[0].Text == "" {
			t.Fatalf("seq %s 的正文被压空了（块流是正文唯一载体）", seq)
		}
		if items[0].MindFSID != turnID {
			t.Fatalf("seq %s 丢了 mindfs_id：%#v", seq, items[0])
		}
	}
	if _, leaked := body.Blocks["1"]; leaked {
		t.Fatalf("窗口外的 seq 1 泄漏进回包")
	}
}

// 没有块流的历史会话必须拿到空 blocks（而不是报错）—— 前端靠这个判据回退旧格式。
// 没有块流的历史会话：第一次读就把老正文**回填成块**并出现在同一个回包里（S2d-1）。
//
// 这条测试在 S2b 时的断言正好相反（want empty）—— 那时「没有块流 → 空 blocks」
// 是前端回退旧格式的契约。S2d 加了回填，契约随之改变：老正文不该再走回退路径，
// 因为回退路径就是「正文可能读不出来」的那条。
//
// 「同一个回包里」是刻意的：回填完再让前端自己拉一次，用户看到的就是先空一屏、
// 再突然冒出正文。
func TestHandleSessionGetBackfillsLegacyBlocks(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Legacy"})
	_ = manager.AddExchangeForAgent(ctx, s, "agent", "老正文", "claude", "", "", "")
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Blocks map[string][]session.Block `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	blocks := body.Blocks["1"]
	if len(blocks) != 1 || blocks[0].Text != "老正文" {
		t.Fatalf("blocks[1] = %#v, want 老正文（回填必须在同一个回包里）", blocks)
	}
}

// 回填之后块流真的落盘了（换个 manager 直读盘），且内容与老主日志一致。
// 只断言响应不足以证明「落盘」—— 回填完全可以每次读都重算，那样只是把
// 猜测从「读时」搬到「读时」，没有变成单一真相源。
func TestBackfillPersistsBlocksToDisk(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Legacy"})
	_ = manager.AddExchangeForAgent(ctx, s, "user", "问题", "claude", "", "", "")
	_ = manager.AddExchangeForAgent(ctx, s, "agent", "老正文", "claude", "", "", "")
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	blocks, err := manager.ReadBlocks(ctx, s.Key)
	if err != nil {
		t.Fatal(err)
	}
	bySeq := session.GroupBlocksBySeq(blocks)
	if got := bySeq[1]; len(got) != 1 || got[0].Text != "问题" {
		t.Fatalf("seq=1 blocks = %#v", got)
	}
	if got := bySeq[2]; len(got) != 1 || got[0].Text != "老正文" {
		t.Fatalf("seq=2 blocks = %#v", got)
	}
}

// 真的什么都没有的会话仍然返回空 map —— 前端的回退分支要留着（它还要服务
// S2d 删 Content 之前的过渡期，以及「纯工具轮」这种本就无正文的轮次）。
func TestHandleSessionGetWithoutAnythingToCompileReturnsEmptyBlocks(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Empty"})
	_ = manager.AddExchangeForAgent(ctx, s, "agent", "", "claude", "", "", "")
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Blocks map[string][]session.Block `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Blocks) != 0 {
		t.Fatalf("blocks = %#v, want empty", body.Blocks)
	}
}

// 块回包的**线上字段名**契约（CUSTOM(G-BH)）。
//
// 为什么需要它：前端 `web/src/services/session.ts` 的 `SessionBlock` 是**手写**的 TS 类型，
// 与 Go 的 `session.Block` 没有编译期联系 —— 改一个 json tag 就能让前端静默读到 undefined，
// 表现为「正文又变空了」，而两端各自的单元测试全绿。
//
// 判据是**精确的键集合**（不是「包含这些键」）：多出的键同样要在这里过一遍，
// 免得顺手加字段时忘了同步 TS 类型。
func TestBlocksPayloadWireFieldNames(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Wire"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	identity := session.WithExchangeIdentity(ctx, session.ExchangeIdentity{
		MindFSID: "11111111-2222-4333-8444-555555555555", TurnSeq: 1,
	})
	if err := manager.AddExchangeForAgent(identity, s, "agent", "", "claude", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}
	if err := manager.AppendBlocks(ctx, s.Key, []session.Block{
		{
			Seq: 1, Pos: 0, Type: session.BlockTypeReasoning, Step: 1,
			MindFSID: "11111111-2222-4333-8444-555555555555", Text: "想",
		},
		{
			Seq: 1, Pos: 1, Type: session.BlockTypeText, Step: 1,
			MindFSID: "11111111-2222-4333-8444-555555555555",
			AgentID:  "native-1", Text: "正文",
		},
		{
			Seq: 1, Pos: 2, Type: session.BlockTypeToolCall, Step: 1,
			MindFSID:        "11111111-2222-4333-8444-555555555555",
			ToolCall:        &agenttypes.ToolCall{CallID: "c1", Title: "t"},
			ParentToolUseID: "p1", TaskID: "task-1", SubagentType: "explore",
		},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Blocks map[string][]map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	items := body.Blocks["1"]
	if len(items) != 3 {
		t.Fatalf("块数 = %d, want 3 body=%s", len(items), rec.Body.String())
	}

	// 键集合 = TS `SessionBlock` 里声明的那一组（omitempty 的字段在该块没值时缺席）。
	want := []map[string]bool{
		{"seq": true, "pos": true, "revision": true, "type": true, "text": true, "step": true, "mindfs_id": true},
		{"seq": true, "pos": true, "revision": true, "type": true, "text": true, "step": true, "mindfs_id": true, "agent_id": true},
		{"seq": true, "pos": true, "revision": true, "type": true, "step": true, "mindfs_id": true, "toolcall": true,
			"parent_tool_use_id": true, "task_id": true, "subagent_type": true},
	}
	// 前端读取用的名字 → 该名字在 Go 侧的出处，红了按这里对照。
	known := map[string]string{
		"seq": "Block.Seq", "pos": "Block.Pos", "revision": "Block.Revision",
		"type": "Block.Type", "text": "Block.Text", "step": "Block.Step",
		"mindfs_id": "Block.MindFSID", "agent_id": "Block.AgentID",
		"toolcall": "Block.ToolCall", "todo": "Block.Todo", "plan": "Block.Plan",
		"compact": "Block.Compact", "parent_tool_use_id": "Block.ParentToolUseID",
		"task_id": "Block.TaskID", "subagent_type": "Block.SubagentType",
	}
	for i, item := range items {
		for key := range item {
			if !want[i][key] {
				t.Errorf("块 %d 多出未登记的键 %q（Go 出处 %s）—— 请同步 web 的 SessionBlock 类型或删掉它",
					i, key, known[key])
			}
		}
		for key := range want[i] {
			if _, ok := item[key]; !ok {
				t.Errorf("块 %d 缺键 %q（Go 出处 %s）—— 前端会读到 undefined，正文会渲染成空",
					i, key, known[key])
			}
		}
	}
	// 正文与工具载荷必须真的在线上，不能被压缩掉（S1 的 CompactBlocksLight 只压工具细节）。
	if items[0]["text"] != "想" || items[1]["text"] != "正文" {
		t.Errorf("正文被压掉了：%v", []any{items[0]["text"], items[1]["text"]})
	}
	if items[2]["toolcall"] == nil {
		t.Errorf("工具载荷丢了：%v", items[2])
	}
}
