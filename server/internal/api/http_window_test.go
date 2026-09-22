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
	resp := h.sessionResponse(s, nil, agenttypes.ContextWindow{}, aux, meta)
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
	resp2 := h.sessionResponse(s, nil, agenttypes.ContextWindow{}, aux, nil)
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

// 复现 2026-09-12 症状 2：点「同步」后正在等待回答的 ask_user 卡（pending, seq=0）消失。
// GET 路径会取 GetPendingUserExchange（http.go:891-894），但 sync 路径硬编码传 nil
// （http.go:1012），于是 sync 响应里没有这条 seq=0 条目 → 前端同步后卡片消失。
func TestHandleSessionSyncCarriesPendingUser(t *testing.T) {
	app, rootID, manager := newWindowTestApp(t)
	ctx := context.Background()
	s, _ := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "SyncPending"})
	if err := manager.AddExchangeForAgent(ctx, s, "user", "hi", "claude", "", "", ""); err != nil {
		t.Fatalf("AddExchange: %v", err)
	}
	// 模拟一轮正在等待 ask_user 回答：hub 里挂着一条尚未落库的 pending user 消息
	app.GetSessionStreamHub().SetPendingUserAt(
		rootID, s.Key, "SyncPending", "claude", "test-model", "", "", "", "", false,
		"pending question", time.Now().UTC(),
	)
	h := &HTTPHandler{AppContext: app}

	rec := doSessionSync(t, h, rootID, s.Key, "")
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
	// pending user 必须以 seq=0 出现在响应里（与 GET 路径同语义）
	foundPending := false
	for _, raw := range exchanges {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		if int(item["seq"].(float64)) == 0 && item["content"] == "pending question" {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("sync response missing pending user (seq=0): %s", rec.Body.String())
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
