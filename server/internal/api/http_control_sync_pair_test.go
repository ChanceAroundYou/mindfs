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

// fakePairing 记录收到的请求并回放预置结果。
type fakePairing struct {
	result controlsync.PairResult
	err    error
	got    []controlsync.PairRequest
}

func (f *fakePairing) Pair(_ context.Context, req controlsync.PairRequest) (controlsync.PairResult, error) {
	f.got = append(f.got, req)
	if f.err != nil {
		return controlsync.PairResult{}, f.err
	}
	return f.result, nil
}

func pairTestHandler(pair ControlSyncPairing) *HTTPHandler {
	return &HTTPHandler{AppContext: &AppContext{}, ControlSyncPair: pair}
}

// pairRequest 造一条回环来源的配对请求；remote 传空表示用默认回环地址。
func pairRequest(body string, remote string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/control-sync/pair", strings.NewReader(body))
	if remote != "" {
		req.RemoteAddr = remote
	}
	return req
}

// 没挂配对能力时不能有这条路由：404 与「配对失败」必须能分开，
// 否则前端分不清「这台机器不支持配对」和「填错了」。
func TestPairRouteAbsentWithoutPairing(t *testing.T) {
	h := pairTestHandler(nil)
	rec := serveHTTP(h.Routes(), pairRequest(`{"primary_url":"https://home:7331"}`, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，未挂配对时应为 404（body=%s）", rec.Code, rec.Body.String())
	}
}

// 配对会改信任锚、写通道密钥、改 config.json —— 三件事凑起来等于在 0.0.0.0 上
// 开放「重定向整份控制面」。所以只接受本机回环来源。
func TestPairRejectsNonLoopback(t *testing.T) {
	pair := &fakePairing{}
	h := pairTestHandler(pair)
	for _, remote := range []string{"192.168.1.10:51234", "10.0.0.2:80", "[::1%25]:999"} {
		rec := serveHTTP(h.Routes(), pairRequest(`{"primary_url":"https://home:7331","token":"0123456789abcdef"}`, remote))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("remote=%s code = %d，非回环应为 403", remote, rec.Code)
		}
	}
	if len(pair.got) != 0 {
		t.Fatalf("非回环请求不该走到配对实现：%+v", pair.got)
	}
}

// 回环来源要放行 —— 上面那条守卫不能把正常路径一起挡掉。
func TestPairAllowsLoopback(t *testing.T) {
	pair := &fakePairing{result: controlsync.PairResult{
		InstanceID:   "hasee",
		PrimaryURL:   "https://home.example:7331",
		PrimaryBuild: "v0.5.5-1300",
		HeadRev:      42,
	}}
	h := pairTestHandler(pair)
	rec := serveHTTP(h.Routes(), pairRequest(
		`{"primary_url":"https://home.example:7331","token":" 0123456789abcdef ","instance_id":"hasee"}`,
		"127.0.0.1:40000"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d，应为 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(pair.got) != 1 {
		t.Fatalf("配对实现被调用 %d 次", len(pair.got))
	}
	// token 前后空白必须在这里就去掉：写进 token 文件的带空白值会在下一次启动时
	// 被 ValidateSyncToken 拒掉，而那时离配对已经隔了一次重启。
	if got := pair.got[0]; got.Token != "0123456789abcdef" || got.InstanceID != "hasee" ||
		got.PrimaryURL != "https://home.example:7331" {
		t.Fatalf("透传字段不对：%+v", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 配完必须重启才生效（路由与同步器都在启动期装配）。不告诉调用方，
	// 界面会显示「已连接」而实际还在未配对状态。
	if body["restart_required"] != true || body["status"] != "paired" {
		t.Fatalf("body = %v，应带 restart_required=true", body)
	}
	if body["primary_version"] != "v0.5.5-1300" || body["instance_id"] != "hasee" {
		t.Fatalf("body = %v，应回显主控端版本与本次实例名", body)
	}
}

func TestPairRejectsBadBody(t *testing.T) {
	h := pairTestHandler(&fakePairing{})
	rec := serveHTTP(h.Routes(), pairRequest(`{"primary_url":`, "127.0.0.1:40000"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d，坏 JSON 应为 400", rec.Code)
	}
}

// 主控端连不上是 502：那是「对端的问题」，不是「你填错了」。
// 前端据此提示「检查主控端地址 / 网络」，而不是让用户反复改 token。
func TestPairMapsUnreachableTo502(t *testing.T) {
	pair := &fakePairing{err: errors.Join(errors.New("probe"), controlsync.ErrPairUnreachable)}
	h := pairTestHandler(pair)
	rec := serveHTTP(h.Routes(), pairRequest(
		`{"primary_url":"https://home.example:7331","token":"0123456789abcdef"}`, "127.0.0.1:40000"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d，应为 502（body=%s）", rec.Code, rec.Body.String())
	}
}

// 其余失败（token 被拒、schema 不匹配、地址不是 https）是 400：调用方要改输入。
func TestPairMapsOtherFailuresTo400(t *testing.T) {
	h := pairTestHandler(&fakePairing{err: errors.New("control_sync: primary rejected this token (401)")})
	rec := serveHTTP(h.Routes(), pairRequest(
		`{"primary_url":"https://home.example:7331","token":"0123456789abcdef"}`, "127.0.0.1:40000"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d，应为 400（body=%s）", rec.Code, rec.Body.String())
	}
}

// 非回环的判定不能信客户端自带的转发头：那类头任何人都能写。
func TestIsLoopbackRequestIgnoresForwardedHeaders(t *testing.T) {
	req := pairRequest("", "192.168.1.10:51234")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")
	if isLoopbackRequest(req) {
		t.Fatal("X-Forwarded-For 不该让请求变成「本机来源」")
	}
	req = pairRequest("", "[::1]:51234")
	if !isLoopbackRequest(req) {
		t.Fatal("IPv6 回环应判定为本机来源")
	}
	req = pairRequest("", "not-an-ip")
	if isLoopbackRequest(req) {
		t.Fatal("解析不出 IP 时应按「不是本机」处理")
	}
}
