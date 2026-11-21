package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/controlsync"
)

type fakePusher struct {
	got   controlsync.PushRequest
	batch controlsync.PushBatch
	err   error
}

func (f *fakePusher) Push(_ context.Context, req controlsync.PushRequest) (controlsync.PushBatch, error) {
	f.got = req
	return f.batch, f.err
}

func TestControlSyncPushEndpoint(t *testing.T) {
	pusher := &fakePusher{batch: controlsync.PushBatch{
		Results: []controlsync.PushResult{{ChangeID: "ch-1", Status: controlsync.PushApplied, Rev: 9}},
		HeadRev: 9,
	}}
	h := &HTTPHandler{ControlSyncPush: pusher}
	body := `{"instance_id":"hasee","base_rev":4,"changes":[{"change_id":"ch-1","domain":"preferences","key":"agents","base_hash":"sha256:x","payload":{"v":2}}]}`
	rec := httptest.NewRecorder()
	h.handleControlSyncPush(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	var got controlsync.PushBatch
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解响应: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Status != controlsync.PushApplied || got.HeadRev != 9 {
		t.Fatalf("响应 = %+v", got)
	}
	if pusher.got.InstanceID != "hasee" || pusher.got.BaseRev != 4 || len(pusher.got.Changes) != 1 {
		t.Fatalf("转给 Pusher 的请求 = %+v", pusher.got)
	}
	if string(pusher.got.Changes[0].Payload) != `{"v":2}` || pusher.got.Changes[0].BaseHash != "sha256:x" {
		t.Fatalf("变更字段没逐字透传：%+v", pusher.got.Changes[0])
	}
}

func TestControlSyncPushEndpointRejectsBadBody(t *testing.T) {
	h := &HTTPHandler{ControlSyncPush: &fakePusher{}}
	rec := httptest.NewRecorder()
	h.handleControlSyncPush(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 body 得到 %d，应为 400", rec.Code)
	}
}

func TestControlSyncPushEndpointReportsPusherFailure(t *testing.T) {
	h := &HTTPHandler{ControlSyncPush: &fakePusher{err: errors.New("db closed")}}
	rec := httptest.NewRecorder()
	h.handleControlSyncPush(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(`{"changes":[]}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Pusher 失败得到 %d，应为 500", rec.Code)
	}
}

func TestControlSyncPushEndpointAbsentWhenNotConfigured(t *testing.T) {
	h := &HTTPHandler{AppContext: &AppContext{}}
	routes := h.Routes()
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未启用 push 时 code = %d，应为 404", rec.Code)
	}
}
