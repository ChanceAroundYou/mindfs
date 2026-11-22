package controlsync

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const authTestToken = "0123456789abcdef-sync-token"

// manualRequest 按给定的时间戳与 nonce 组一个已签名请求：随机 nonce 没法测重放，
// 所以这里绕开 SignSyncRequest 直接写头，签名仍走被测的 SyncSignature。
func manualRequest(method, target, body, token, timestamp, nonce string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(SyncTimestampHeader, timestamp)
	req.Header.Set(SyncNonceHeader, nonce)
	req.Header.Set(SyncSignatureHeader, SyncSignature(token, timestamp, nonce, []byte(body)))
	return req
}

func verifyAt(req *http.Request, body string, cache *NonceCache, now time.Time) error {
	return VerifySyncRequest(req, []byte(body), SyncVerifyOptions{
		Token:  authTestToken,
		Nonces: cache,
		Now:    func() time.Time { return now },
	})
}

func TestSignSyncRequestRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	body := `{"changes":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/control-sync/push", strings.NewReader(body))
	if err := SignSyncRequest(req, authTestToken, "hasee", []byte(body), now); err != nil {
		t.Fatalf("SignSyncRequest: %v", err)
	}
	for _, header := range []string{
		"Authorization", SyncInstanceHeader, SyncTimestampHeader, SyncNonceHeader, SyncSignatureHeader,
	} {
		if strings.TrimSpace(req.Header.Get(header)) == "" {
			t.Fatalf("签名后缺少头 %s", header)
		}
	}
	if got := req.Header.Get(SyncInstanceHeader); got != "hasee" {
		t.Fatalf("instance 头 = %q", got)
	}
	if err := verifyAt(req, body, NewNonceCache(SyncNonceTTL), now.Add(2*time.Second)); err != nil {
		t.Fatalf("刚签的请求应通过：%v", err)
	}
}

func TestSignSyncRequestRequiresToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/control-sync/head", nil)
	if err := SignSyncRequest(req, "  ", "hasee", nil, time.Now()); err == nil {
		t.Fatal("空 token 不应能签名")
	}
}

func TestVerifySyncRequestRejectsMissingHeaders(t *testing.T) {
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	nonce := "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"没带 bearer", func(r *http.Request) { r.Header.Del("Authorization") }},
		{"没带时间戳", func(r *http.Request) { r.Header.Del(SyncTimestampHeader) }},
		{"没带 nonce", func(r *http.Request) { r.Header.Del(SyncNonceHeader) }},
		{"没带签名", func(r *http.Request) { r.Header.Del(SyncSignatureHeader) }},
		{"时间戳不是 RFC3339", func(r *http.Request) { r.Header.Set(SyncTimestampHeader, "yesterday") }},
		{"nonce 太短", func(r *http.Request) { r.Header.Set(SyncNonceHeader, "abc") }},
		{"签名不是 hex", func(r *http.Request) { r.Header.Set(SyncSignatureHeader, "zzzz") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := manualRequest(http.MethodGet, "/api/control-sync/head", "", authTestToken, ts, nonce)
			tc.mutate(req)
			if err := verifyAt(req, "", NewNonceCache(SyncNonceTTL), now); err == nil {
				t.Fatal("缺头/坏头必须拒绝")
			}
		})
	}
}

func TestVerifySyncRequestRejectsWrongSignature(t *testing.T) {
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	nonce := "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		req  *http.Request
		body string
	}{
		{
			name: "用了别的 token 签",
			req:  manualRequest(http.MethodGet, "/api/control-sync/head", "", "another-token-0123456789", ts, nonce),
		},
		{
			name: "签名被改",
			req: func() *http.Request {
				r := manualRequest(http.MethodGet, "/api/control-sync/head", "", authTestToken, ts, nonce)
				r.Header.Set(SyncSignatureHeader, strings.Repeat("0", 64))
				return r
			}(),
		},
		{
			// 签名必须覆盖 body：只签空体却发一个非空载荷，是最容易漏的一种。
			name: "body 不在签名里",
			req:  manualRequest(http.MethodPost, "/api/control-sync/push", "", authTestToken, ts, nonce),
			body: `{"changes":[]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyAt(tc.req, tc.body, NewNonceCache(SyncNonceTTL), now); err == nil {
				t.Fatal("签名不符必须拒绝")
			}
		})
	}
}

func TestVerifySyncRequestEnforcesClockSkew(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		ok   bool
	}{
		{"窗口内", now.Add(-4 * time.Minute), true},
		{"刚好提前窗口", now.Add(-SyncClockSkew), true},
		{"提前过头", now.Add(-SyncClockSkew - time.Second), false},
		{"往后过头", now.Add(SyncClockSkew + time.Second), false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := tc.at.UTC().Format(time.RFC3339)
			nonce := fmt.Sprintf("nonce-skew-%02d", i) // 每个用例独立 nonce，避免重放判定干扰
			req := manualRequest(http.MethodGet, "/api/control-sync/head", "", authTestToken, ts, nonce)
			err := verifyAt(req, "", NewNonceCache(SyncNonceTTL), now)
			if tc.ok && err != nil {
				t.Fatalf("窗口内应通过：%v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("超出时间窗必须拒绝")
			}
		})
	}
}

func TestVerifySyncRequestRejectsReplay(t *testing.T) {
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	nonce := "0123456789abcdef0123456789abcdef"
	cache := NewNonceCache(SyncNonceTTL)

	first := manualRequest(http.MethodPost, "/api/control-sync/push", `{"changes":[]}`, authTestToken, ts, nonce)
	if err := verifyAt(first, `{"changes":[]}`, cache, now); err != nil {
		t.Fatalf("首次请求应通过：%v", err)
	}
	second := manualRequest(http.MethodPost, "/api/control-sync/push", `{"changes":[]}`, authTestToken, ts, nonce)
	if err := verifyAt(second, `{"changes":[]}`, cache, now.Add(time.Second)); err == nil {
		t.Fatal("同一 nonce 重放必须拒绝")
	}
}

// 被拒的请求不能占用 nonce：否则攻击者随手发一个坏签名就能把合法请求的重放位烧掉。
func TestVerifySyncRequestDoesNotConsumeNonceOnBadSignature(t *testing.T) {
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	nonce := "0123456789abcdef0123456789abcdef"
	cache := NewNonceCache(SyncNonceTTL)

	// bearer 必须是对的，否则会先被 bearer 挡掉，测不到「nonce 有没有被占用」。
	bad := httptest.NewRequest(http.MethodGet, "/api/control-sync/head", nil)
	bad.Header.Set("Authorization", "Bearer "+authTestToken)
	bad.Header.Set(SyncTimestampHeader, ts)
	bad.Header.Set(SyncNonceHeader, nonce)
	bad.Header.Set(SyncSignatureHeader,
		SyncSignature("another-token-0123456789", ts, nonce, nil))
	if err := verifyAt(bad, "", cache, now); err == nil {
		t.Fatal("坏签名应被拒绝")
	}
	good := manualRequest(http.MethodGet, "/api/control-sync/head", "", authTestToken, ts, nonce)
	if err := verifyAt(good, "", cache, now); err != nil {
		t.Fatalf("坏签名不应占用 nonce：%v", err)
	}
}

func TestNonceCacheExpiresAndStaysBounded(t *testing.T) {
	now := time.Now()
	cache := NewNonceCache(time.Minute)
	if !cache.Accept("nonce-1", now) {
		t.Fatal("首次应接受")
	}
	if cache.Accept("nonce-1", now.Add(30*time.Second)) {
		t.Fatal("TTL 内重复必须拒绝")
	}
	if !cache.Accept("nonce-1", now.Add(2*time.Minute)) {
		t.Fatal("过了 TTL 应可再次接受（缓存已清理）")
	}

	// 上限：塞满之后仍必须是有限内存。
	for i := 0; i < syncNonceCacheLimit+10; i++ {
		cache.Accept(fmt.Sprintf("bulk-nonce-%08d", i), now)
	}
	cache.mu.Lock()
	size := len(cache.seen)
	cache.mu.Unlock()
	if size > syncNonceCacheLimit {
		t.Fatalf("nonce 缓存 %d 条，超过上限 %d", size, syncNonceCacheLimit)
	}
}
