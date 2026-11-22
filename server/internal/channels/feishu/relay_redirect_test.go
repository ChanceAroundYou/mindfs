package feishu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 跨主机跳转必须让调用失败：默认 http.Client 会把自定义请求头一起带过去，
// 于是一个被控制的 redirect 目标就能拿到中继 token。
func TestNewRelayClientRefusesCrossHostRedirect(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	var reached, leaked atomic.Int32
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if r.Header.Get(RelayTokenHeader) == token {
			leaked.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer decoy.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, decoy.URL+"/steal", http.StatusFound)
	}))
	defer first.Close()

	client := NewRelayClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, first.URL+"/send", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(RelayTokenHeader, token)
	resp, err := client.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		t.Fatal("跨主机 redirect 必须作为网络错误返回，不能把 token 带过去")
	}
	if reached.Load() != 0 || leaked.Load() != 0 {
		t.Fatalf("decoy reached=%d token leaked=%d", reached.Load(), leaked.Load())
	}
}

func TestNewRelayClientAllowsSameHostRedirect(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	var finalCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			if r.Header.Get(RelayTokenHeader) == token {
				finalCalls.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewRelayClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/start", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(RelayTokenHeader, token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || finalCalls.Load() != 1 {
		t.Fatalf("status=%d finalCalls=%d", resp.StatusCode, finalCalls.Load())
	}
}
