package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
	agenttypes "mindfs/server/internal/agent/types"
)

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
