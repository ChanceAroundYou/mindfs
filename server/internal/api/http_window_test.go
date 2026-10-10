package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
	agenttypes "mindfs/server/internal/agent/types"
)

func TestSessionResponseWindowFiltersAux(t *testing.T) {
	h := &HTTPHandler{}
	s := &session.Session{
		Key: "k1",
		Exchanges: []session.Exchange{
			{Seq: 2, Content: "b"},
			{Seq: 3, Content: "c"},
		},
	}
	aux := map[int][]session.ExchangeAux{
		1: {{Seq: 1, Thought: "t1"}},
		2: {{Seq: 2, Thought: "t2"}},
		3: {{Seq: 3, Thought: "t3"}},
		4: {{Seq: 4, Thought: "t4"}},
	}
	// window mode: only seq 2,3 should remain
	meta := &session.SessionWindowMeta{Total: 4, HasMore: true, MinSeq: 2, MaxSeq: 3}
	resp := h.sessionResponse(s, agenttypes.ContextWindow{}, aux, nil, meta)
	auxPayload, ok := resp["exchange_aux"].(map[string][]session.ExchangeAux)
	if !ok {
		t.Fatalf("exchange_aux type = %T", resp["exchange_aux"])
	}
	if _, ok := auxPayload["1"]; ok {
		t.Fatalf("seq 1 should be filtered, got %#v", auxPayload)
	}
	if _, ok := auxPayload["4"]; ok {
		t.Fatalf("seq 4 should be filtered")
	}
	if len(auxPayload["2"]) == 0 || len(auxPayload["3"]) == 0 {
		t.Fatalf("seq 2,3 should be present: %#v", auxPayload)
	}
	if _, ok := resp["window_meta"]; !ok {
		t.Fatalf("window_meta missing in window mode")
	}
	// non-window mode: all seqs pass through
	resp2 := h.sessionResponse(s, agenttypes.ContextWindow{}, aux, nil, nil)
	auxPayload2 := resp2["exchange_aux"].(map[string][]session.ExchangeAux)
	if len(auxPayload2) != 4 {
		t.Fatalf("non-window aux len = %d, want 4", len(auxPayload2))
	}
	if _, ok := resp2["window_meta"]; ok {
		t.Fatalf("window_meta should be absent in non-window mode")
	}
}

func newWindowTestApp(t *testing.T) (*AppContext, string, *session.Manager) {
	t.Helper()
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "project"), 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}
	registry := fs.NewRegistry(filepath.Join(parent, "registry.json"))
	projectPath := filepath.Join(parent, "project")
	dir, err := registry.Upsert(projectPath)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	app := &AppContext{Dirs: registry}
	manager, err := app.GetSessionManager(dir.ID)
	if err != nil {
		t.Fatalf("GetSessionManager: %v", err)
	}
	return app, dir.ID, manager
}

func doSessionGet(t *testing.T, h *HTTPHandler, rootID, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+key+"?root="+rootID+query, nil)
	// inject chi param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("key", key)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handleSessionGet(rec, req)
	return rec
}

func TestHandleSessionGetWindowLatest(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Win"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange %d: %v", i, err)
		}
	}
	// 5 exchanges seq 1..5
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	meta, ok := body["window_meta"].(map[string]any)
	if !ok {
		t.Fatalf("window_meta missing: %s", rec.Body.String())
	}
	if int(meta["total"].(float64)) != 5 {
		t.Fatalf("total = %v, want 5", meta["total"])
	}
	if !meta["hasMore"].(bool) {
		t.Fatalf("hasMore false, want true")
	}
	exchanges, ok := body["exchanges"].([]any)
	if !ok || len(exchanges) != 2 {
		t.Fatalf("exchanges len = %d, want 2 body=%s", len(exchanges), rec.Body.String())
	}
}

func TestHandleSessionGetBeforeSeq(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Win2"})
	for i := 0; i < 6; i++ {
		_ = manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", "")
	}
	h := &HTTPHandler{AppContext: app}
	// before_seq=4 limit=2 → seq 2,3
	rec := doSessionGet(t, h, rootID, s.Key, "&before_seq=4&limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	exchanges := body["exchanges"].([]any)
	if len(exchanges) != 2 {
		t.Fatalf("exchanges len = %d, want 2", len(exchanges))
	}
	// verify seqs are 2 and 3 (first exchange seq 2)
	first := exchanges[0].(map[string]any)
	if int(first["seq"].(float64)) != 2 {
		t.Fatalf("first seq = %v, want 2", first["seq"])
	}
	meta := body["window_meta"].(map[string]any)
	if !meta["hasMore"].(bool) {
		t.Fatalf("hasMore false, want true (still items before)")
	}
	// before_seq at beginning → empty window not error
	rec2 := doSessionGet(t, h, rootID, s.Key, "&before_seq=1&limit=2")
	if rec2.Code != http.StatusOK {
		t.Fatalf("before_seq=1 status = %d", rec2.Code)
	}
	var body2 map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &body2)
	if exs, ok := body2["exchanges"].([]any); !ok || len(exs) != 0 {
		t.Fatalf("before_seq=1 exchanges = %v, want []", body2["exchanges"])
	}
}

func TestHandleSessionGetMutualExclusion(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Win3"})
	_ = manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", "")
	h := &HTTPHandler{AppContext: app}
	rec := doSessionGet(t, h, rootID, s.Key, "&seq=1&latest=2")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mutual exclusion status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	rec2 := doSessionGet(t, h, rootID, s.Key, "&seq=1&before_seq=2")
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("seq+before_seq status = %d, want 400", rec2.Code)
	}
}

func TestHandleSessionGetLimitClamp(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Win4"})
	for i := 0; i < 3; i++ {
		_ = manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", "")
	}
	h := &HTTPHandler{AppContext: app}
	// limit=999 should clamp to 200 and still succeed (returns all 3)
	rec := doSessionGet(t, h, rootID, s.Key, "&latest=2&limit=999")
	if rec.Code != http.StatusOK {
		t.Fatalf("limit clamp status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	exchanges := body["exchanges"].([]any)
	if len(exchanges) != 2 {
		t.Fatalf("clamped latest=2 len=%d, want 2", len(exchanges))
	}
	// default limit (no limit param) should be 50
	rec2 := doSessionGet(t, h, rootID, s.Key, "&latest=1")
	if rec2.Code != http.StatusOK {
		t.Fatalf("default limit status = %d", rec2.Code)
	}
}

func doSessionSync(t *testing.T, h *HTTPHandler, rootID, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+key+"/sync?root="+rootID+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("key", key)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handleSessionSync(rec, req)
	return rec
}

func TestHandleSessionSyncWindow(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "WinSync"})
	for i := 0; i < 4; i++ {
		_ = manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", "")
	}
	h := &HTTPHandler{AppContext: app}
	rec := doSessionSync(t, h, rootID, s.Key, "&latest=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync window status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["window_meta"]; !ok {
		t.Fatalf("sync window_meta missing: %s", rec.Body.String())
	}
	// mutual exclusion on sync too
	rec2 := doSessionSync(t, h, rootID, s.Key, "&seq=1&latest=1")
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("sync mutual exclusion status = %d, want 400", rec2.Code)
	}
}

// 复现 2026-09-28 症状：agent 执行中，用户自己那条消息从对话列表消失、点「同步」也刷不回来。
//
// 旧实现：user 行到回合结束才落盘，回合进行中读接口只能返回 StreamHub 的 pendingUser 内存
// 副本，且 sessionResponse 强制把它降级成 seq=0。客户端却已按广播的「预测 seq」认定该行持久化，
// overlay 的「seq<=latestSeq 即让位给窗口」规则在任意一次重锚定时把它丢掉。
//
// 新实现：user 行在回合开始前就落盘，读接口直接返回它自己的真 seq。
func TestHandleSessionSyncCarriesInFlightUserWithRealSeq(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "SyncPending"})
	if err := manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
		t.Fatalf("AddExchange: %v", err)
	}
	// 回合进行中：user 行已落盘、助手行还没写。这是本 bug 的时间窗。
	if err := manager.AddExchangeForAgentAt(ctx, s, "user", "pending question", "claude", "", "", "", time.Now().UTC()); err != nil {
		t.Fatalf("AddExchangeForAgentAt(user): %v", err)
	}
	h := &HTTPHandler{AppContext: app}

	// 走窗口模式（latest=20），这样响应带 window_meta——前端正是拿它的 maxSeq 做
	// 「seq<=latestSeq 即让位给窗口」的判定。
	rec := doSessionSync(t, h, rootID, s.Key, "&latest=20")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	exchanges, _ := body["exchanges"].([]any)
	if len(exchanges) == 0 {
		t.Fatalf("exchanges empty: %s", rec.Body.String())
	}
	// 本轮 user 行必须带**真实 seq**（>0）出现，且只出现一次。
	seen := 0
	for _, raw := range exchanges {
		item, _ := raw.(map[string]any)
		if item == nil || item["content"] != "pending question" {
			continue
		}
		seen++
		if seq := int(item["seq"].(float64)); seq <= 0 {
			t.Fatalf("in-flight user row must carry a real seq, got %d: %s", seq, rec.Body.String())
		}
	}
	if seen != 1 {
		t.Fatalf("in-flight user row must appear exactly once, got %d: %s", seen, rec.Body.String())
	}
	// 窗口 maxSeq 必须已覆盖本轮 user 行：否则前端「seq<=latestSeq 让位」仍会丢它。
	meta, _ := body["window_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("window_meta missing: %s", rec.Body.String())
	}
	maxSeq := int(meta["maxSeq"].(float64))
	found := false
	for _, raw := range exchanges {
		item, _ := raw.(map[string]any)
		if item != nil && item["content"] == "pending question" && int(item["seq"].(float64)) == maxSeq {
			found = true
		}
	}
	if !found {
		t.Fatalf("window maxSeq=%d must cover the in-flight user row: %s", maxSeq, rec.Body.String())
	}
}

// 双写重复行必须在 API 读取出口折叠：保留实时路径那条，导入那条隐藏；且导入行的 aux
// 要重挂到保留行上（aux 以 seq 为键，丢了工具卡就没了）。
func TestHandleSessionGetProjectsDuplicateExchanges(t *testing.T) {
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
	if len(payload.Exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1: %+v", len(payload.Exchanges), payload.Exchanges)
	}
	if payload.Exchanges[0].Source != session.ExchangeSourceLive {
		t.Fatalf("保留的必须是实时路径写的行: %+v", payload.Exchanges[0])
	}
	if len(payload.Aux["1"]) == 0 {
		t.Fatalf("导入行的 aux 必须重挂到保留行 seq=1: %+v", payload.Aux)
	}
	if _, ok := payload.Aux["2"]; ok {
		t.Fatalf("被隐藏行的 aux 键不应再出现: %+v", payload.Aux)
	}
}

// 「同步」的目的永远是刷新会话视图：外部转录同步失败（转录找不到、导入器不可用等）不得
// 把整个请求变成 400。GET 路径一直是这个语义，这里曾经把错误直接透传成 400 —— 实测
// 2026-09-22 会话 1790064991（agent 跑在随后被移除的 .worktree 里，转录仍在残留的 slug
// 目录下）因此「同步」永久报 external session not found。
func TestHandleSessionSyncExternalFailureStillReturnsWindow(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "SyncFail"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
		t.Fatalf("AddExchange: %v", err)
	}
	current, err := manager.Get(ctx, s.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// 绑定一个不存在的外部会话：同步必然在外部转录这一步失败。
	if err := manager.UpdateAgentState(ctx, current, "claude", 0, "no-such-external-session"); err != nil {
		t.Fatalf("UpdateAgentState: %v", err)
	}
	h := &HTTPHandler{AppContext: app}
	rec := doSessionSync(t, h, rootID, s.Key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("外部同步失败时 status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["exchanges"]; !ok {
		t.Fatalf("会话内容必须照常返回: %s", rec.Body.String())
	}
}

// 窗口化的 sync 必须走「窗口内 seq 的 aux」这条读路径（尾部有界读 + 轻压缩），
// 而不是无条件 Seq=0 全量读整个 aux 文件再按窗口过滤。
//
// 判据用 G-AT 的轻压缩契约：窗口路径对 edit/read/execute 清空 content（展开时懒加载），
// 非窗口路径保留 content。两者在响应里可直接观察。
func TestHandleSessionSyncWindowUsesWindowAux(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "SyncAuxWin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange: %v", err)
		}
	}
	if err := manager.AddExchangeAux(ctx, s.Key, session.ExchangeAux{
		Seq: 2,
		ToolCall: &agenttypes.ToolCall{
			CallID: "call-edit",
			Kind:   agenttypes.ToolKindEdit,
			Title:  "edit",
			Content: []agenttypes.ToolCallContentItem{
				{Type: "text", Text: "diff --git a/x b/x"},
			},
		},
	}); err != nil {
		t.Fatalf("add aux: %v", err)
	}
	h := &HTTPHandler{AppContext: app}

	auxContent := func(rec *httptest.ResponseRecorder) (bool, bool) {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var body struct {
			Aux map[string][]session.ExchangeAux `json:"exchange_aux"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
		}
		entries := body.Aux["2"]
		if len(entries) == 0 || entries[0].ToolCall == nil {
			return false, false
		}
		return true, len(entries[0].ToolCall.Content) > 0
	}

	present, hasContent := auxContent(doSessionSync(t, h, rootID, s.Key, "&latest=2"))
	if !present {
		t.Fatalf("窗口响应缺少 seq=2 的 aux")
	}
	if hasContent {
		t.Fatalf("窗口路径必须轻压缩（edit content 清空），实际保留了 content")
	}

	present, hasContent = auxContent(doSessionSync(t, h, rootID, s.Key, ""))
	if !present {
		t.Fatalf("非窗口响应缺少 seq=2 的 aux")
	}
	if !hasContent {
		t.Fatalf("非窗口路径必须保留 edit content（重锚定不闪内容）")
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
			session.WithExchangeIdentity(ctx, session.ExchangeIdentity{TurnID: turnID, TurnSeq: i}),
			s, "agent", "", "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange %d: %v", i, err)
		}
		// 每轮都只有 reasoning（本 bug 的形态）：没有 text 块，正文只在块流里。
		if err := manager.AppendBlocks(ctx, s.Key, []session.Block{{
			Seq: i, Pos: 0, Type: session.BlockTypeReasoning, Step: 1, TurnID: turnID,
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
		if items[0].TurnID != turnID {
			t.Fatalf("seq %s 丢了 turn_id：%#v", seq, items[0])
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
		TurnID: "11111111-2222-4333-8444-555555555555", TurnSeq: 1,
	})
	if err := manager.AddExchangeForAgent(identity, s, "agent", "", "claude", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}
	if err := manager.AppendBlocks(ctx, s.Key, []session.Block{
		{
			Seq: 1, Pos: 0, Type: session.BlockTypeReasoning, Step: 1,
			TurnID: "11111111-2222-4333-8444-555555555555", Text: "想",
		},
		{
			Seq: 1, Pos: 1, Type: session.BlockTypeText, Step: 1,
			TurnID:   "11111111-2222-4333-8444-555555555555",
			NativeID: "native-1", Text: "正文",
		},
		{
			Seq: 1, Pos: 2, Type: session.BlockTypeToolCall, Step: 1,
			TurnID:          "11111111-2222-4333-8444-555555555555",
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
		{"seq": true, "pos": true, "revision": true, "type": true, "text": true, "step": true, "turn_id": true},
		{"seq": true, "pos": true, "revision": true, "type": true, "text": true, "step": true, "turn_id": true, "native_id": true},
		{"seq": true, "pos": true, "revision": true, "type": true, "step": true, "turn_id": true, "toolcall": true,
			"parent_tool_use_id": true, "task_id": true, "subagent_type": true},
	}
	// 前端读取用的名字 → 该名字在 Go 侧的出处，红了按这里对照。
	known := map[string]string{
		"seq": "Block.Seq", "pos": "Block.Pos", "revision": "Block.Revision",
		"type": "Block.Type", "text": "Block.Text", "step": "Block.Step",
		"turn_id": "Block.TurnID", "native_id": "Block.NativeID",
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
