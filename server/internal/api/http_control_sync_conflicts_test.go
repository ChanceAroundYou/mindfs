package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/controlsync"
)

type fakeConflicts struct {
	views      []controlsync.ConflictView
	listErr    error
	resolveErr error

	gotID      string
	gotChoice  string
	gotPayload []byte
}

func (f *fakeConflicts) List(context.Context) ([]controlsync.ConflictView, error) {
	return f.views, f.listErr
}

func (f *fakeConflicts) Resolve(_ context.Context, id, choice string, payload []byte) error {
	f.gotID, f.gotChoice, f.gotPayload = id, choice, payload
	return f.resolveErr
}

func newConflictTestServer(t *testing.T, c ControlSyncConflicts) http.Handler {
	t.Helper()
	h := &HTTPHandler{AppContext: &AppContext{}}
	if c != nil {
		h.ControlSyncConflicts = c
	}
	return h.Routes()
}

func TestControlSyncConflictsListEndpoint(t *testing.T) {
	f := &fakeConflicts{views: []controlsync.ConflictView{{
		ID: "pull:users:u_1", Domain: "users", Key: "u_1", Redacted: true,
		LocalHash: "sha256:a", RemoteHash: "sha256:b",
	}}}
	srv := newConflictTestServer(t, f)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/conflicts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var got struct {
		Conflicts []controlsync.ConflictView `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解响应: %v", err)
	}
	if len(got.Conflicts) != 1 || got.Conflicts[0].ID != "pull:users:u_1" || !got.Conflicts[0].Redacted {
		t.Fatalf("响应 = %+v", got.Conflicts)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("响应里出现了口令字段：%s", rec.Body.String())
	}

	empty := newConflictTestServer(t, &fakeConflicts{})
	rec = httptest.NewRecorder()
	empty.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/control-sync/conflicts", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"conflicts":[]}` {
		t.Fatalf("空列表应回 [], 得到 %d %s", rec.Code, rec.Body.String())
	}
}

func TestControlSyncConflictResolveEndpoint(t *testing.T) {
	f := &fakeConflicts{}
	srv := newConflictTestServer(t, f)
	body := `{"choice":"payload","payload":{"v":"merged"}}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/control-sync/conflicts/pull:preferences:agents/resolve", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if f.gotID != "pull:preferences:agents" || f.gotChoice != "payload" {
		t.Fatalf("转给 Resolver 的请求 = id=%q choice=%q", f.gotID, f.gotChoice)
	}
	if string(f.gotPayload) != `{"v":"merged"}` {
		t.Fatalf("payload 没逐字透传：%s", f.gotPayload)
	}
}

func TestControlSyncConflictResolveStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"没这张卡", controlsync.ErrConflictNotFound, http.StatusNotFound},
		{"已经裁决过", controlsync.ErrConflictResolved, http.StatusConflict},
		{"选择不合法", fmt.Errorf("%w: %q", controlsync.ErrConflictChoice, "merge"), http.StatusBadRequest},
		{"意外错误", errors.New("磁盘满了"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConflictTestServer(t, &fakeConflicts{resolveErr: tc.err})
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
				"/api/control-sync/conflicts/pull:preferences:agents/resolve",
				strings.NewReader(`{"choice":"local"}`)))
			if rec.Code != tc.code {
				t.Fatalf("code = %d，应为 %d（body=%s）", rec.Code, tc.code, rec.Body.String())
			}
		})
	}
}

func TestControlSyncConflictRoutesAbsentWhenNotConfigured(t *testing.T) {
	srv := newConflictTestServer(t, nil)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/control-sync/conflicts", nil),
		httptest.NewRequest(http.MethodPost, "/api/control-sync/conflicts/x/resolve", strings.NewReader(`{"choice":"local"}`)),
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s 得到 %d，应为 404", req.Method, req.URL.Path, rec.Code)
		}
	}
}