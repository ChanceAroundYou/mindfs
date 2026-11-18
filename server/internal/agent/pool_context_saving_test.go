package agent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent/proxy"
)

// 下面两个用例钉住 B2 的跨包链路：池级 session key（带 agent 前缀）必须原样
// 传给代理账本，否则前端按会话读到的永远是空摘要 —— 症状是 chip 不出现，
// 而不是报错，所以只能靠测试。

func TestPoolSessionContextSavingForwardsPoolKey(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	ps, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{
		Mode: proxy.LiveCompactOn, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	p := &Pool{compactProxy: ps}
	token := "pool-key-token"
	postPoolFold(t, ps, token, "claude-session-1")

	// 池里的 key 就是代理记的 key：带 agent 前缀的那个。
	if got := p.SessionContextSaving("claude-session-1"); got.Requests != 1 || got.Latest.SavedBytes <= 0 {
		t.Fatalf("pool key summary = %+v", got)
	}
	// 不带前缀的 key 是另一条账本，不能串。
	if got := p.SessionContextSaving("session-1"); got.Requests != 0 {
		t.Fatalf("bare session key aliased onto the agent-prefixed ledger: %+v", got)
	}
}

// 没有代理（live-compact 未装配 / 启动失败）时返回空摘要而不是 panic：池是每个节点
// 都会有的，代理不是。
func TestPoolSessionContextSavingWithoutProxy(t *testing.T) {
	p := &Pool{}
	if got := p.SessionContextSaving("anything"); got.Requests != 0 {
		t.Fatalf("summary without a proxy = %+v", got)
	}
	var nilPool *Pool
	if got := nilPool.SessionContextSaving("anything"); got.Requests != 0 {
		t.Fatalf("nil pool summary = %+v", got)
	}
}

// archive dir 必须真的从启动选项传进代理：忘了传时 mode=on 只观测不折叠，
// 账本永远为空，而日志里只有一行「observing only」，极难发现。
func TestNewPoolWithOptionsWiresArchiveDir(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	p := NewPoolWithOptions(Config{}, PoolOptions{
		LiveCompactMode:       proxy.LiveCompactOn,
		LiveCompactArchiveDir: archiveDir,
	})
	t.Cleanup(p.CloseAll)
	if p.compactProxy == nil {
		t.Fatal("proxy not started")
	}
	t.Cleanup(func() { _ = p.compactProxy.Close() })
	// 用真实请求证明折叠生效：只有 archive 那一路会被写。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "datan\n")
	}))
	defer upstream.Close()

	postPoolFoldTo(t, p.compactProxy, upstream.URL, "pool-ledger-token", "claude-wired")
	if got := p.SessionContextSaving("claude-wired"); got.Requests != 1 {
		t.Fatalf("pool did not wire the archive dir into the proxy: %+v", got)
	}
}

// postPoolFold 让代理自己产生一条账本记录：注册普通会话再发一个可折叠请求。
func postPoolFold(t *testing.T, ps *proxy.Server, token, sessionKey string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	postPoolFoldTo(t, ps, upstream.URL, token, sessionKey)
}

func postPoolFoldTo(t *testing.T, ps *proxy.Server, upstreamURL, token, sessionKey string) {
	t.Helper()
	ps.RegisterSessionWithKey(token, upstreamURL, sessionKey)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", ps.Port(), token),
		"application/json",
		strings.NewReader(poolFoldBody(4, 8001)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// poolFoldBody 是与代理测试同形的最小可折叠请求（4 轮、每轮 8 KB 工具输出）。
func poolFoldBody(rounds, size int) string {
	var builder strings.Builder
	builder.WriteString(`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"start"}]}`)
	for index := 0; index < rounds; index++ {
		id := fmt.Sprintf("toolu_%d", index)
		builder.WriteString(fmt.Sprintf(`,{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/x"}}]}`, id))
		builder.WriteString(fmt.Sprintf(`,{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":false}]}`, id, strings.Repeat("a", size)))
	}
	builder.WriteString("]}")
	return builder.String()
}
