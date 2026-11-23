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

// fakeEdges 用内存表模拟主节点名单，行为与 controlsync.Store 的语义对齐：
// 已登记只推进 last_seen_at；被踢掉返回行 + ErrEdgeRevoked 且不动 last_seen_at。
type fakeEdges struct {
	rows     map[string]controlsync.EdgeInfo
	observed []string
}

func newFakeEdges() *fakeEdges { return &fakeEdges{rows: map[string]controlsync.EdgeInfo{}} }

func (f *fakeEdges) ObserveEdge(_ context.Context, id string, at time.Time) (controlsync.EdgeInfo, error) {
	f.observed = append(f.observed, id)
	row, ok := f.rows[id]
	if !ok {
		row = controlsync.EdgeInfo{InstanceID: id, RegisteredAt: at}
	}
	if row.Revoked() {
		return row, controlsync.ErrEdgeRevoked
	}
	row.LastSeenAt = at
	f.rows[id] = row
	return row, nil
}

func (f *fakeEdges) ListEdges(context.Context) ([]controlsync.EdgeInfo, error) {
	out := make([]controlsync.EdgeInfo, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeEdges) RevokeEdge(_ context.Context, id string, at time.Time) (controlsync.EdgeInfo, error) {
	row, ok := f.rows[id]
	if !ok {
		return controlsync.EdgeInfo{}, controlsync.ErrEdgeNotFound
	}
	if !row.Revoked() {
		row.RevokedAt = &at
	}
	f.rows[id] = row
	return row, nil
}

func (f *fakeEdges) RestoreEdge(_ context.Context, id string) (controlsync.EdgeInfo, error) {
	row, ok := f.rows[id]
	if !ok {
		return controlsync.EdgeInfo{}, controlsync.ErrEdgeNotFound
	}
	row.RevokedAt = nil
	f.rows[id] = row
	return row, nil
}

// newEdgesTestServer 造一个带边控端名单的主节点 handler。
func newEdgesTestServer(edges ControlSyncEdges) *HTTPHandler {
	return &HTTPHandler{
		AppContext:       &AppContext{},
		ControlSyncFeed:  &fakeFeed{head: 3},
		ControlSyncPush:  &fakePusher{},
		ControlSyncEdges: edges,
		ControlSyncToken: feedTestToken,
	}
}

// signedInstanceRequest 是 signedSyncRequest 加一个 X-MindFS-Instance 头。
func signedInstanceRequest(method, path, body, instanceID string) *http.Request {
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	nonce := strings.ReplaceAll(now.Format("20060102150405.000000000"), ".", "") + "abcdef012345"
	req := signedSyncRequest(method, path, body, feedTestToken, feedTestToken, timestamp, nonce)
	if instanceID != "" {
		req.Header.Set(controlsync.SyncInstanceHeader, instanceID)
	}
	return req
}

// TestSyncRequestAutoRegistersEdge 钉「自动注册」这一半需求：
// 边缘不需要额外调一次 /register，任何一条过了签名的请求都要在名单里出现。
func TestSyncRequestAutoRegistersEdge(t *testing.T) {
	edges := newFakeEdges()
	h := newEdgesTestServer(edges)

	rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	if _, ok := edges.rows["hasee"]; !ok {
		t.Fatalf("名单里没有 hasee：observed=%v", edges.observed)
	}

	// 第二次请求只推进 last_seen_at，不新增行。
	rec = serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/changes?since=0", "", "hasee"))
	if rec.Code != http.StatusOK {
		t.Fatalf("changes code = %d", rec.Code)
	}
	if len(edges.rows) != 1 {
		t.Fatalf("名单 = %v，同一台边控端不该出现两行", edges.rows)
	}
}

// TestSyncRequestWithoutInstanceIsNotRegistered 钉另一半：没带 instance 头时
// 不登记也不拦。那是改造前的客户端形态，token 已经校验过了。
func TestSyncRequestWithoutInstanceIsNotRegistered(t *testing.T) {
	edges := newFakeEdges()
	h := newEdgesTestServer(edges)
	rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，应为 200", rec.Code)
	}
	if len(edges.rows) != 0 {
		t.Fatalf("名单 = %v，没带 instance 头不该登记", edges.rows)
	}
}

// TestRevokedEdgeGets403 钉踢人真的生效：名单不是只读展示。
func TestRevokedEdgeGets403(t *testing.T) {
	edges := newFakeEdges()
	h := newEdgesTestServer(edges)
	serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	if _, err := edges.RevokeEdge(context.Background(), "hasee", time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	before := edges.rows["hasee"].LastSeenAt

	rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d，被踢掉的边控端应为 403（body=%s）", rec.Code, rec.Body.String())
	}
	if edges.rows["hasee"].LastSeenAt != before {
		t.Fatal("被拒绝的请求推进了 last_seen_at")
	}

	// 恢复后同一台机器能继续同步。
	if _, err := edges.RestoreEdge(context.Background(), "hasee"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rec = serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	if rec.Code != http.StatusOK {
		t.Fatalf("恢复后 code = %d，应为 200", rec.Code)
	}
}

// TestEdgesListAndRevokeEndpoints 钉 HTTP 面：列表、踢人、恢复三条。
// 全部走 requireSyncToken —— 能改名单就等于能让一台机器的同步停摆。
func TestEdgesListAndRevokeEndpoints(t *testing.T) {
	edges := newFakeEdges()
	h := newEdgesTestServer(edges)
	serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "fnbox"))

	t.Run("列表", func(t *testing.T) {
		rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/edges", "", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Edges []controlsync.EdgeInfo `json:"edges"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Edges) != 2 {
			t.Fatalf("edges = %+v，应有两条", got.Edges)
		}
	})

	t.Run("踢人", func(t *testing.T) {
		rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodPost, "/api/control-sync/edges/hasee/revoke", "", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if !edges.rows["hasee"].Revoked() {
			t.Fatal("revoke 之后 revoked_at 仍为空")
		}
	})

	t.Run("踢不存在的边控端是 404", func(t *testing.T) {
		rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodPost, "/api/control-sync/edges/ghost/revoke", "", ""))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("code = %d，应为 404", rec.Code)
		}
	})

	t.Run("恢复", func(t *testing.T) {
		rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodPost, "/api/control-sync/edges/hasee/restore", "", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if edges.rows["hasee"].Revoked() {
			t.Fatal("restore 之后仍带 revoked_at")
		}
	})

	t.Run("无签名一律 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/control-sync/edges", nil)
		if rec := serveHTTP(h.Routes(), req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d，应为 401", rec.Code)
		}
	})
}

// TestEdgesEndpointAbsentWithoutWiring 钉「没启用同步的机器不暴露这个端点」。
func TestEdgesEndpointAbsentWithoutWiring(t *testing.T) {
	h := (&HTTPHandler{AppContext: &AppContext{}}).Routes()
	rec := serveHTTP(h, httptest.NewRequest(http.MethodGet, "/api/control-sync/edges", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，应为 404", rec.Code)
	}
}

// TestNoteRequestingEdgeToleratesListFailure 钉「名单写失败不拦同步」：
// 它是运维索引，不是数据通路的一环。
type brokenEdges struct{ fakeEdges }

func (b *brokenEdges) ObserveEdge(context.Context, string, time.Time) (controlsync.EdgeInfo, error) {
	return controlsync.EdgeInfo{}, errors.New("disk full")
}

func TestNoteRequestingEdgeToleratesListFailure(t *testing.T) {
	h := newEdgesTestServer(&brokenEdges{fakeEdges: *newFakeEdges()})
	rec := serveHTTP(h.Routes(), signedInstanceRequest(http.MethodGet, "/api/control-sync/head", "", "hasee"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，名单写失败不该拦同步", rec.Code)
	}
}
