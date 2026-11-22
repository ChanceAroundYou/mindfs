package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
)

type fakeControlSync struct {
	status      controlsync.Status
	statusErr   error
	localCalls  int
	retryCalls  int
	retryMode   controlsync.Mode
	retryErr    error
	adoptCalls  int
	adoptErr    error
	lastErrText string
}

func (f *fakeControlSync) Status(context.Context) (controlsync.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeControlSync) SwitchLocal(context.Context) error {
	f.localCalls++
	f.status.Mode = controlsync.ModeOfflineLocal
	return nil
}

func (f *fakeControlSync) Retry(context.Context) (controlsync.Mode, error) {
	f.retryCalls++
	if f.retryErr != nil {
		f.status.Mode = controlsync.ModeOfflineLocal
		f.status.LastError = f.retryErr.Error()
		return controlsync.ModeOfflineLocal, f.retryErr
	}
	f.status.Mode = f.retryMode
	return f.retryMode, nil
}

func (f *fakeControlSync) AdoptPrimary(context.Context) (controlsync.Mode, error) {
	f.adoptCalls++
	if f.adoptErr != nil {
		return controlsync.ModeOfflineLocal, f.adoptErr
	}
	f.status.Mode = controlsync.ModeReconciling
	return controlsync.ModeReconciling, nil
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) controlsync.Status {
	t.Helper()
	var got controlsync.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解 status: %v（body=%s）", err, rec.Body.String())
	}
	return got
}

func TestControlSyncStatusEndpoint(t *testing.T) {
	svc := &fakeControlSync{status: controlsync.Status{
		Mode:          controlsync.ModeOfflineLocal,
		BaseRev:       42,
		LastPrimaryAt: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
		LastError:     "dial tcp: refused",
	}}
	h := &HTTPHandler{ControlSync: svc}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d，应为 200", rec.Code)
	}
	got := decodeStatus(t, rec)
	if got.Mode != controlsync.ModeOfflineLocal || got.BaseRev != 42 || got.LastError == "" {
		t.Fatalf("status 载荷 = %+v，字段不完整", got)
	}
	if got.LastPrimaryAt.IsZero() {
		t.Fatalf("last_primary_at 丢了")
	}
}

func TestControlSyncModeEndpointActions(t *testing.T) {
	svc := &fakeControlSync{retryMode: controlsync.ModeReconciling}
	h := &HTTPHandler{ControlSync: svc}

	rec := httptest.NewRecorder()
	h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(`{"action":"local"}`)))
	if rec.Code != http.StatusOK || svc.localCalls != 1 {
		t.Fatalf("local 动作：code=%d calls=%d", rec.Code, svc.localCalls)
	}
	if got := decodeStatus(t, rec); got.Mode != controlsync.ModeOfflineLocal {
		t.Fatalf("local 动作后 status.mode = %s", got.Mode)
	}

	rec = httptest.NewRecorder()
	h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(`{"action":"retry"}`)))
	if rec.Code != http.StatusOK || svc.retryCalls != 1 {
		t.Fatalf("retry 动作：code=%d calls=%d", rec.Code, svc.retryCalls)
	}
	if got := decodeStatus(t, rec); got.Mode != controlsync.ModeReconciling {
		t.Fatalf("retry 成功后 status.mode = %s，应为 reconciling", got.Mode)
	}
}

// 主节点探不通：动作被接受（200），原因写在 last_error，前端据 status 渲染即可。
func TestControlSyncModeRetryFailureStillReturnsStatus(t *testing.T) {
	svc := &fakeControlSync{retryErr: errors.New("probe: connection refused")}
	h := &HTTPHandler{ControlSync: svc}
	rec := httptest.NewRecorder()
	h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(`{"action":"retry"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("retry 失败时 code = %d，应仍是 200", rec.Code)
	}
	got := decodeStatus(t, rec)
	if got.Mode != controlsync.ModeOfflineLocal || got.LastError == "" {
		t.Fatalf("retry 失败后 status = %+v，应停在 offline-local 并带原因", got)
	}
}

// 主节点被换掉之后总得有一条有记录的出路：采纳当前主节点，丢掉旧副本重来。
func TestControlSyncModeAdoptPrimaryAction(t *testing.T) {
	svc := &fakeControlSync{}
	h := &HTTPHandler{ControlSync: svc}
	rec := httptest.NewRecorder()
	h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(`{"action":"adopt_primary"}`)))
	if rec.Code != http.StatusOK || svc.adoptCalls != 1 {
		t.Fatalf("adopt_primary：code=%d calls=%d", rec.Code, svc.adoptCalls)
	}
	if got := decodeStatus(t, rec); got.Mode != controlsync.ModeReconciling {
		t.Fatalf("采纳后 status.mode = %s，应为 reconciling", got.Mode)
	}

	// 重置副本失败是真错误（用户以为采纳了，其实没成），不能照 200 回。
	svc = &fakeControlSync{adoptErr: errors.New("disk full")}
	h = &HTTPHandler{ControlSync: svc}
	rec = httptest.NewRecorder()
	h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(`{"action":"adopt_primary"}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("采纳失败 code = %d，应为 500", rec.Code)
	}
}

func TestControlSyncModeEndpointRejectsBadAction(t *testing.T) {
	svc := &fakeControlSync{}
	h := &HTTPHandler{ControlSync: svc}
	for _, body := range []string{`{"action":"online"}`, `{}`, `not json`} {
		rec := httptest.NewRecorder()
		h.handleControlSyncMode(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/mode", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q 得到 code=%d，应为 400", body, rec.Code)
		}
	}
	if svc.localCalls != 0 || svc.retryCalls != 0 {
		t.Fatalf("非法动作仍调到了服务：local=%d retry=%d", svc.localCalls, svc.retryCalls)
	}
}

func TestControlSyncEndpointsAbsentWhenNotConfigured(t *testing.T) {
	h := &HTTPHandler{AppContext: &AppContext{}}
	routes := h.Routes()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/control-sync/status"},
		{http.MethodPost, "/api/control-sync/mode"},
	} {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s 未启用时 code=%d，应为 404", tc.method, tc.path, rec.Code)
		}
	}
}

func TestControlSyncStatusEndpointReportsStoreError(t *testing.T) {
	h := &HTTPHandler{ControlSync: &fakeControlSync{statusErr: errors.New("db closed")}}
	rec := httptest.NewRecorder()
	h.handleControlSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/status", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status 读库失败时 code=%d，应为 500", rec.Code)
	}
}
