package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/preferences"

	"github.com/gorilla/websocket"
)

// 跨节点 WS 是 fork 的多节点特性：页面由控制节点提供，WS 直连 worker 节点，
// Origin 与 Host 必然不同。gorilla 默认的同源判定会把每一次连接都拒成 403
// （body 只有 10 字节 "Forbidden"），远端节点的会话就打不开、正文空白、发不出消息。
// 放行口径必须和 REST 层同一套 CORS 策略，不能各写一份。
func TestWSCrossOriginUpgradeFollowsCORSPolicy(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		allowed []string
		origin  string
		wantOK  bool
	}{
		{"默认 open 放行任意来源", "", nil, "https://home.example", true},
		{"allowlist 命中", "allowlist", []string{"https://home.example"}, "https://home.example", true},
		{"allowlist 未命中", "allowlist", []string{"https://other.example"}, "https://home.example", false},
		{"disabled 全拒", "disabled", nil, "https://home.example", false},
		{"无 Origin 的脚本客户端放行", "disabled", nil, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := preferences.NewStoreAt(t.TempDir())
			if err != nil {
				t.Fatalf("preferences.NewStoreAt: %v", err)
			}
			if err := store.UpdateCORSPreferences(tc.mode, tc.allowed); err != nil {
				t.Fatalf("UpdateCORSPreferences: %v", err)
			}

			server := httptest.NewServer(&WSHandler{AppContext: &AppContext{Prefs: store}})
			defer server.Close()

			header := http.Header{}
			if tc.origin != "" {
				header.Set("Origin", tc.origin)
			}
			url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?client_id=test"
			conn, resp, err := websocket.DefaultDialer.Dial(url, header)
			if conn != nil {
				_ = conn.Close()
			}

			if gotOK := err == nil; gotOK != tc.wantOK {
				status := 0
				if resp != nil {
					status = resp.StatusCode
				}
				t.Fatalf("upgrade ok=%v status=%d, want ok=%v", gotOK, status, tc.wantOK)
			}
			if !tc.wantOK && resp != nil && resp.StatusCode != http.StatusForbidden {
				t.Fatalf("rejected with %d, want 403", resp.StatusCode)
			}
		})
	}
}

func TestCORSPolicyAllowsCoversEveryMode(t *testing.T) {
	listed := func(origin string) bool { return origin == "https://ok.example" }
	cases := []struct {
		mode   string
		origin string
		want   bool
	}{
		{"", "https://any.example", true},
		{"open", "https://any.example", true},
		{"ALLOW_ALL", "https://any.example", true},
		{" allowlist ", "https://ok.example", true},
		{"allowlist", "https://no.example", false},
		{"disabled", "https://ok.example", false},
		{"same_origin", "https://ok.example", false},
	}
	for _, tc := range cases {
		if got := corsPolicyAllows(tc.mode, tc.origin, listed); got != tc.want {
			t.Fatalf("corsPolicyAllows(%q, %q) = %v, want %v", tc.mode, tc.origin, got, tc.want)
		}
	}
}
