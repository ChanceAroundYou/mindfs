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

type fakeFeed struct {
	head       int64
	instanceID string
	build      string
	changes    []controlsync.RemoteChange
	err        error
	gotSince   int64
	gotLimit   int
	pageCalled int
}

func (f *fakeFeed) Head(context.Context) (controlsync.HeadInfo, error) {
	return controlsync.HeadInfo{
		HeadRev:       f.head,
		InstanceID:    f.instanceID,
		BuildVersion:  f.build,
		SchemaVersion: controlsync.SyncSchemaVersion,
	}, f.err
}

func (f *fakeFeed) Page(_ context.Context, since int64, limit int) ([]controlsync.RemoteChange, int64, error) {
	f.pageCalled++
	f.gotSince, f.gotLimit = since, limit
	return f.changes, f.head, f.err
}

const feedTestToken = "0123456789abcdef-token"

func newFeedTestServer(f ControlSyncFeed) http.Handler {
	return (&HTTPHandler{
		AppContext:       &AppContext{},
		ControlSyncFeed:  f,
		ControlSyncPush:  &fakePusher{},
		ControlSyncToken: feedTestToken,
	}).Routes()
}

func serve(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	payload := ""
	if method == http.MethodPost {
		// /push 的鉴权在解析 body 之前，给一个能过解析的最小载荷。
		payload = `{"changes":[]}`
	}
	req := httptest.NewRequest(method, path, strings.NewReader(payload))
	if token != "" {
		// 认证已经不是「带个 bearer 就行」：请求必须整体签名，签名覆盖 body。
		if err := controlsync.SignSyncRequest(req, token, "feed-test", []byte(payload), time.Now()); err != nil {
			panic(err)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// 没配 token 就不注册同步端点。这些端点能整包读出口令 hash，默认必须是 404 而不是
// 「注册了但没人鉴权」。
func TestControlSyncEndpointsAbsentWithoutToken(t *testing.T) {
	h := (&HTTPHandler{
		AppContext:      &AppContext{},
		ControlSyncFeed: &fakeFeed{},
		ControlSyncPush: &fakePusher{},
	}).Routes()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/control-sync/head"},
		{http.MethodGet, "/api/control-sync/changes"},
		{http.MethodPost, "/api/control-sync/push"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("未配 token 时 %s %s = %d，应为 404", tc.method, tc.path, rec.Code)
		}
	}
}

func TestControlSyncEndpointsRequireBearerToken(t *testing.T) {
	h := newFeedTestServer(&fakeFeed{head: 7})
	for _, tc := range []struct {
		name  string
		token string
		code  int
	}{
		{"没带 token", "", http.StatusUnauthorized},
		{"token 不对", "ffffffffffffffff", http.StatusUnauthorized},
		{"正确 token", feedTestToken, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/api/control-sync/head", "/api/control-sync/changes"} {
				rec := serve(h, http.MethodGet, path, tc.token)
				if rec.Code != tc.code {
					t.Fatalf("%s = %d，应为 %d（body=%s）", path, rec.Code, tc.code, rec.Body.String())
				}
				if tc.code == http.StatusUnauthorized {
					if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
						t.Fatalf("%s 401 缺少 WWW-Authenticate：%q", path, got)
					}
				}
			}
			rec := serve(h, http.MethodPost, "/api/control-sync/push", tc.token)
			if rec.Code != tc.code {
				t.Fatalf("/push = %d，应为 %d", rec.Code, tc.code)
			}
		})
	}
}

func TestControlSyncHeadEndpoint(t *testing.T) {
	rec := serve(newFeedTestServer(&fakeFeed{head: 42, instanceID: "home", build: "v1.2.3"}),
		http.MethodGet, "/api/control-sync/head", feedTestToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
	}
	// 身份字段是边缘识别「主节点被重建」的唯一依据，形状属于线上契约。
	var got struct {
		HeadRev       int64  `json:"head_rev"`
		InstanceID    string `json:"instance_id"`
		BuildVersion  string `json:"build_version"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解响应: %v", err)
	}
	if got.HeadRev != 42 {
		t.Fatalf("head_rev = %d，应为 42", got.HeadRev)
	}
	if got.InstanceID != "home" || got.BuildVersion != "v1.2.3" {
		t.Fatalf("身份 = %q/%q", got.InstanceID, got.BuildVersion)
	}
	if got.SchemaVersion != controlsync.SyncSchemaVersion {
		t.Fatalf("schema_version = %d", got.SchemaVersion)
	}
}

func TestControlSyncChangesEndpointPassesWatermark(t *testing.T) {
	f := &fakeFeed{head: 30, changes: []controlsync.RemoteChange{{
		Rev: 12, Domain: "preferences", Key: "agents",
		Hash: "sha256:x", Payload: json.RawMessage(`{"v":2}`), Origin: "home",
	}}}
	h := newFeedTestServer(f)

	rec := serve(h, http.MethodGet, "/api/control-sync/changes?since=12&limit=50", feedTestToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if f.gotSince != 12 || f.gotLimit != 50 {
		t.Fatalf("since/limit 透传成 %d/%d", f.gotSince, f.gotLimit)
	}
	var got struct {
		Changes []controlsync.RemoteChange `json:"changes"`
		HeadRev int64                      `json:"head_rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解响应: %v", err)
	}
	if len(got.Changes) != 1 || got.Changes[0].Rev != 12 || string(got.Changes[0].Payload) != `{"v":2}` {
		t.Fatalf("变更页 = %+v", got.Changes)
	}
	if got.HeadRev != 30 {
		t.Fatalf("head_rev = %d，应为 30", got.HeadRev)
	}

	// 空页必须是 []，不能是 null：边缘把它解成 nil 切片后判「无变更」才有意义，
	// 而前端/日志里 null 看起来更像出错。
	empty := newFeedTestServer(&fakeFeed{head: 30})
	rec = serve(empty, http.MethodGet, "/api/control-sync/changes?since=30", feedTestToken)
	if !strings.Contains(rec.Body.String(), `"changes":[]`) {
		t.Fatalf("空页响应 = %s，应为 changes:[]", rec.Body.String())
	}
}

func TestControlSyncChangesEndpointRejectsBadParams(t *testing.T) {
	h := newFeedTestServer(&fakeFeed{})
	for _, q := range []string{"?since=abc", "?since=-1", "?limit=0", "?limit=-5", "?limit=1.5"} {
		rec := serve(h, http.MethodGet, "/api/control-sync/changes"+q, feedTestToken)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 得到 %d，应为 400", q, rec.Code)
		}
	}
}

func TestControlSyncFeedEndpointReportsFailure(t *testing.T) {
	rec := serve(newFeedTestServer(&fakeFeed{err: errors.New("db closed")}), http.MethodGet, "/api/control-sync/head", feedTestToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d，应为 500", rec.Code)
	}
}

// 保留窗口缺口不是 5xx：主节点本身没坏，是这份副本落得太远、重试也不会好。
// 折成 500 会让边缘把它当暂时故障一直重试，而正确处置是当场停下等人裁决。
func TestControlSyncChangesEndpointReportsRetentionGap(t *testing.T) {
	feed := &fakeFeed{err: controlsync.ErrRetentionGap}
	rec := serve(newFeedTestServer(feed), http.MethodGet, "/api/control-sync/changes?since=1", feedTestToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d（body=%s），应为 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "retention") {
		t.Fatalf("响应 = %s，应写明保留窗口缺口", rec.Body.String())
	}
}
