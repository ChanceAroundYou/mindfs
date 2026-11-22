package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
)

// signedSyncRequest 手工组请求头：重放用例必须复用同一组 timestamp/nonce，
// 而 SignSyncRequest 每次都会生成新的随机 nonce。
func signedSyncRequest(method, path, body, signToken, bearer, timestamp, nonce string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set(controlsync.SyncTimestampHeader, timestamp)
	req.Header.Set(controlsync.SyncNonceHeader, nonce)
	req.Header.Set(controlsync.SyncSignatureHeader,
		controlsync.SyncSignature(signToken, timestamp, nonce, []byte(body)))
	return req
}

func serveHTTP(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// 只有 bearer 已经不够了：spes §6.3 / D4 要求带时间戳、nonce 与 HMAC 签名。
// 这一组用例逐个拆掉一个要素，确认每一层都真的在挡。
func TestControlSyncEndpointsRequireSignedRequest(t *testing.T) {
	h := newFeedTestServer(&fakeFeed{head: 7})
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	nonce := "0123456789abcdef0123456789abcdef"
	body := `{"changes":[]}`

	t.Run("只有 bearer 没有签名", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/control-sync/head", nil)
		req.Header.Set("Authorization", "Bearer "+feedTestToken)
		if rec := serveHTTP(h, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d，应为 401", rec.Code)
		}
	})

	t.Run("bearer 对但签名用了别的 token", func(t *testing.T) {
		req := signedSyncRequest(http.MethodPost, "/api/control-sync/push", body,
			"another-token-0123456789", feedTestToken, timestamp, nonce)
		if rec := serveHTTP(h, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d，应为 401", rec.Code)
		}
	})

	t.Run("签名没覆盖 body", func(t *testing.T) {
		// 用空 body 签名，实际发一份非空载荷：漏签 body 的实现会放行。
		req := httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+feedTestToken)
		req.Header.Set(controlsync.SyncTimestampHeader, timestamp)
		req.Header.Set(controlsync.SyncNonceHeader, nonce)
		req.Header.Set(controlsync.SyncSignatureHeader,
			controlsync.SyncSignature(feedTestToken, timestamp, nonce, nil))
		if rec := serveHTTP(h, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d，应为 401", rec.Code)
		}
	})

	t.Run("时间戳过期", func(t *testing.T) {
		stale := now.Add(-controlsync.SyncClockSkew - time.Minute).Format(time.RFC3339)
		req := signedSyncRequest(http.MethodGet, "/api/control-sync/head", "",
			feedTestToken, feedTestToken, stale, nonce)
		if rec := serveHTTP(h, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d，应为 401", rec.Code)
		}
	})

	t.Run("同一请求重放", func(t *testing.T) {
		build := func() *http.Request {
			return signedSyncRequest(http.MethodGet, "/api/control-sync/head", "",
				feedTestToken, feedTestToken, timestamp, nonce)
		}
		if rec := serveHTTP(h, build()); rec.Code != http.StatusOK {
			t.Fatalf("首次 code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
		}
		if rec := serveHTTP(h, build()); rec.Code != http.StatusUnauthorized {
			t.Fatalf("重放 code = %d，应为 401", rec.Code)
		}
	})

	t.Run("超过 8 MiB", func(t *testing.T) {
		huge := strings.Repeat("x", controlsync.SyncMaxBodyBytes+1)
		req := httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(huge))
		if rec := serveHTTP(h, req); rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("code = %d，应为 413", rec.Code)
		}
	})
}

// 中间件为了验签必须把 body 读空，所以必须原样放回：否则验签通过之后，
// 下游的 push handler 会收到一份空载荷（或半份），表现为「鉴权成功但什么都没写」。
func TestControlSyncPushRoutesPreserveSignedBody(t *testing.T) {
	pusher := &fakePusher{batch: controlsync.PushBatch{HeadRev: 3}}
	h := &HTTPHandler{AppContext: &AppContext{}, ControlSyncToken: feedTestToken, ControlSyncPush: pusher}
	body := `{"instance_id":"pc-win","changes":[{"change_id":"c9","domain":"preferences","key":"agents","payload":{"v":2}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(body))
	if err := controlsync.SignSyncRequest(req, feedTestToken, "pc-win", []byte(body), time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	rec := serveHTTP(h.Routes(), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(pusher.got.Changes) != 1 || pusher.got.Changes[0].ChangeID != "c9" {
		t.Fatalf("下游收到的请求 = %+v", pusher.got)
	}
}
