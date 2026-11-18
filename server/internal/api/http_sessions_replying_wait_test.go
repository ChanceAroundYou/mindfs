package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
)

// newReplyingWaitTestHandler 复刻既有 handler 测试的装配方式：只给 AppContext 一个空
// registry，hub 由 GetSessionStreamHub 惰性创建。会话用未注册的 rootID（"root-1"），
// 于是载荷构造里的 GetRoot 快速失败、不碰 SQLite —— 用例跑得快且不落盘。
func newReplyingWaitTestHandler(t *testing.T) (*HTTPHandler, *StreamHub) {
	t.Helper()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	app := &AppContext{Dirs: registry}
	h := &HTTPHandler{AppContext: app}
	hub := app.GetSessionStreamHub()
	// 在途计数是包级全局状态：每个用例起手清零、收尾也清零，避免用例之间互相污染。
	atomic.StoreInt32(&replyingWaitInFlight, 0)
	t.Cleanup(func() { atomic.StoreInt32(&replyingWaitInFlight, 0) })
	return h, hub
}

// replyingWaitTuning 临时改写包级计时参数，用例结束一律还原。
func replyingWaitTuning(t *testing.T, poll, refresh, min, def, max time.Duration) {
	t.Helper()
	oldPoll, oldRefresh := replyingWaitPollInterval, replyingWaitRefreshInterval
	oldMin, oldDef, oldMax := replyingWaitMinTimeout, replyingWaitDefaultTimeout, replyingWaitMaxTimeout
	replyingWaitPollInterval, replyingWaitRefreshInterval = poll, refresh
	replyingWaitMinTimeout, replyingWaitDefaultTimeout, replyingWaitMaxTimeout = min, def, max
	t.Cleanup(func() {
		replyingWaitPollInterval, replyingWaitRefreshInterval = oldPoll, oldRefresh
		replyingWaitMinTimeout, replyingWaitDefaultTimeout, replyingWaitMaxTimeout = oldMin, oldDef, oldMax
	})
}

// seedReplyingWaitETag 发一次不带 If-None-Match 的请求，取回客户端播种用的 ETag。
func seedReplyingWaitETag(t *testing.T, h *HTTPHandler) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("播种请求 status=%d body=%s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("播种请求缺少 ETag")
	}
	return etag
}

func TestReplyingWaitFirstRequestReturnsImmediatelyWithoutETag(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	replyingWaitTuning(t, 200*time.Millisecond, 50*time.Millisecond, 5*time.Second, 20*time.Second, 30*time.Second)
	hub.SetPendingReply("root-1", "s-a", "A")

	start := time.Now()
	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil))
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("首次请求必须立即返回，实际耗时 %s", elapsed)
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"s-a"`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == "" {
		t.Fatal("首帧必须带 ETag 供客户端播种")
	}
}

func TestReplyingWaitReturnsOnShapeChange(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	replyingWaitTuning(t, 10*time.Millisecond, time.Second, 5*time.Second, 20*time.Second, 30*time.Second)
	hub.SetPendingReply("root-1", "s-a", "A")
	etag := seedReplyingWaitETag(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.handleReplyingSessionsWait(w, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	hub.SetPendingReply("root-1", "s-b", "B")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("形状变化未在 1s 内唤醒等待")
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"s-b"`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReplyingWaitTimeoutReturns304WhenUnchanged(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	// 三段超时都压到 50ms：URL 仍传秒级 timeout=1，验证夹取把 1s 收进 [min,max]。
	replyingWaitTuning(t, 10*time.Millisecond, time.Second, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	hub.SetPendingReply("root-1", "s-a", "A")
	etag := seedReplyingWaitETag(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=1", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()

	start := time.Now()
	h.handleReplyingSessionsWait(w, req)
	elapsed := time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Fatalf("超时未生效、提前返回：%s", elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("超时用例过慢：%s", elapsed)
	}
	if w.Code != http.StatusNotModified {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("304 不应带正文：%q", w.Body.String())
	}
}

func TestReplyingWaitSummaryOnlyChangeWaitsForRefresh(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	// 形状不变，只有摘要/updatedAt 变：必须等到刷新间隔才返回。
	replyingWaitTuning(t, 10*time.Millisecond, 300*time.Millisecond, 5*time.Second, 20*time.Second, 30*time.Second)
	hub.SetPendingReply("root-1", "s-a", "A")
	etag := seedReplyingWaitETag(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		h.handleReplyingSessionsWait(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	hub.AppendReplyEvent("s-a", StreamEvent{Type: "message_chunk", Data: agenttypes.MessageChunk{Content: "新摘要"}})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("摘要变化未在 1s 内返回")
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("摘要变化不应早于刷新间隔返回：%s", elapsed)
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "新摘要") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReplyingWaitRefreshElapsedUnchangedKeepsWaiting(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	// 刷新间隔 50ms、超时 500ms，且全程没有任何状态变化：handler 必须一直等到超时，
	// 不能在刷新间隔到点时就返回 —— 载荷签名没变就不该唤醒客户端。
	// 少了这条用例，删掉 replyingPayloadSignature 的比对（每个 refresh 都返回一次）也全绿。
	replyingWaitTuning(t, 10*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond, 500*time.Millisecond, time.Second)
	hub.SetPendingReply("root-1", "s-a", "A")
	etag := seedReplyingWaitETag(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()

	start := time.Now()
	h.handleReplyingSessionsWait(w, req)
	elapsed := time.Since(start)

	// 若回归成「刷新间隔一到就返回」，这里会是 ≈50ms。
	if elapsed < 450*time.Millisecond {
		t.Fatalf("刷新间隔到点但载荷未变时不应提前返回：%s", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("超时用例过慢：%s", elapsed)
	}
	// 载荷与 If-None-Match 完全一致 → respondJSONConditional 回 304 且无正文。
	if w.Code != http.StatusNotModified {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("304 不应带正文：%q", w.Body.String())
	}
}

func TestReplyingWaitCancelledContextReturns(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	replyingWaitTuning(t, 10*time.Millisecond, time.Second, 5*time.Second, 20*time.Second, 30*time.Second)
	hub.SetPendingReply("root-1", "s-a", "A")
	etag := seedReplyingWaitETag(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=20", nil).WithContext(ctx)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.handleReplyingSessionsWait(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("上下文取消后 handler 未退出")
	}
	if w.Body.Len() != 0 {
		t.Fatalf("取消不应写响应：%q", w.Body.String())
	}
}

func TestReplyingWaitRejectsWhenInFlightFull(t *testing.T) {
	h, _ := newReplyingWaitTestHandler(t)
	atomic.StoreInt32(&replyingWaitInFlight, replyingWaitMaxInFlight)

	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q，应为 3", got)
	}
	// 503 路径也必须回滚在途计数，否则一次拒绝就把余量永久吃掉。
	if got := atomic.LoadInt32(&replyingWaitInFlight); got != replyingWaitMaxInFlight {
		t.Fatalf("在途计数未回滚：%d，应为 %d", got, replyingWaitMaxInFlight)
	}
}

func TestReplyingWaitTimeoutClamp(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"1", replyingWaitMinTimeout},       // 1s < min → 夹到 min
		{"999", replyingWaitMaxTimeout},     // 999s > max → 夹到 max
		{"abc", replyingWaitDefaultTimeout}, // 非法 → 默认
		{"", replyingWaitDefaultTimeout},    // 缺省 → 默认
		{"20", 20 * time.Second},            // 合法范围内原样
	} {
		if got := parseReplyingWaitTimeout(tc.raw); got != tc.want {
			t.Fatalf("parseReplyingWaitTimeout(%q)=%s，应为 %s", tc.raw, got, tc.want)
		}
	}
}
