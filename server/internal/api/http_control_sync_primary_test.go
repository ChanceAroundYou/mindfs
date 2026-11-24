package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
)

type fakeControlSyncPrimary struct {
	status controlsync.PrimaryStatus
	err    error
	calls  int
}

func (f *fakeControlSyncPrimary) PrimaryStatus(context.Context) (controlsync.PrimaryStatus, error) {
	f.calls++
	return f.status, f.err
}

func decodePrimaryStatus(t *testing.T, rec *httptest.ResponseRecorder) controlsync.PrimaryStatus {
	t.Helper()
	var got controlsync.PrimaryStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解 primary status: %v（body=%s）", err, rec.Body.String())
	}
	return got
}

// 主节点没有 ModeController，但同步确实在跑：/status 必须给得出答案，
// 而且要能让读者一眼看出这是主节点视角（role）而不是边缘的四态。
func TestControlSyncStatusFallsBackToPrimarySummary(t *testing.T) {
	seen := time.Date(2026, 11, 24, 10, 40, 0, 0, time.UTC)
	primary := &fakeControlSyncPrimary{status: controlsync.PrimaryStatus{
		Role:            "primary",
		HeadRev:         121,
		SchemaVersion:   controlsync.SyncSchemaVersion,
		EdgeCount:       2,
		ActiveEdgeCount: 1,
		LastEdgeSeenAt:  &seen,
		ConflictCount:   3,
		CheckedAt:       seen,
	}}
	h := &HTTPHandler{ControlSyncPrimary: primary}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	got := decodePrimaryStatus(t, rec)
	if got.Role != "primary" || got.HeadRev != 121 || got.EdgeCount != 2 ||
		got.ActiveEdgeCount != 1 || got.ConflictCount != 3 {
		t.Fatalf("primary status 载荷 = %+v", got)
	}
	if got.LastEdgeSeenAt == nil || !got.LastEdgeSeenAt.Equal(seen) {
		t.Fatalf("last_edge_seen_at = %v，应为 %v", got.LastEdgeSeenAt, seen)
	}
	if got.CheckedAt.IsZero() {
		t.Fatalf("checked_at 丢了")
	}
}

// 边缘有自己的四态，优先级必须高于主节点摘要 —— 一台机器不可能同时是两者，
// 但装配出错时宁可显示边缘真相，也不要用主节点摘要把断线状态盖掉。
func TestControlSyncStatusPrefersEdgeOverPrimary(t *testing.T) {
	svc := &fakeControlSync{status: controlsync.Status{Mode: controlsync.ModeOfflineLocal, BaseRev: 7}}
	primary := &fakeControlSyncPrimary{status: controlsync.PrimaryStatus{Role: "primary", HeadRev: 999}}
	h := &HTTPHandler{ControlSync: svc, ControlSyncPrimary: primary}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d，应为 200", rec.Code)
	}
	got := decodeStatus(t, rec)
	if got.Mode != controlsync.ModeOfflineLocal || got.BaseRev != 7 {
		t.Fatalf("应返回边缘状态，实际 = %+v", got)
	}
	if primary.calls != 0 {
		t.Fatalf("边缘模式下不该查主节点摘要，实际调用 %d 次", primary.calls)
	}
}

// 两边都没有 = 这台机器确实不同步。仍是 404，前端据此停表并转查 /pair。
func TestControlSyncStatusStill404WhenNothingConfigured(t *testing.T) {
	h := &HTTPHandler{}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status code = %d，应为 404", rec.Code)
	}
}

// 路由必须真的把这条路径挂上：handler 写对了但路由只在边缘注册，
// 主节点上照样 404 —— 这正是用户看到的现象。
func TestControlSyncStatusRouteRegisteredForPrimary(t *testing.T) {
	primary := &fakeControlSyncPrimary{status: controlsync.PrimaryStatus{Role: "primary", HeadRev: 42}}
	h := &HTTPHandler{AppContext: &AppContext{}, ControlSyncPrimary: primary}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	if got := decodePrimaryStatus(t, rec); got.Role != "primary" || got.HeadRev != 42 {
		t.Fatalf("载荷 = %+v", got)
	}
	// /mode 是边缘独有的写动作，主节点上必须仍然没有。
	rec = httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("主节点上的 /mode code=%d，应为 404", rec.Code)
	}
}

// 主节点还没有任何边控端连过时，摘要照常 200 —— 空名单不是错误。
func TestControlSyncStatusRouteEmptyPrimaryIs200(t *testing.T) {
	primary := &fakeControlSyncPrimary{status: controlsync.PrimaryStatus{Role: "primary"}}
	h := &HTTPHandler{AppContext: &AppContext{}, ControlSyncPrimary: primary}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
}

// 读库失败不能伪装成「一切正常」：半份摘要比没有摘要更糟。
func TestControlSyncPrimaryStatusReadFailureIs500(t *testing.T) {
	primary := &fakeControlSyncPrimary{err: errors.New("disk I/O error")}
	h := &HTTPHandler{ControlSyncPrimary: primary}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d，应为 500（body=%s）", rec.Code, rec.Body.String())
	}
}
