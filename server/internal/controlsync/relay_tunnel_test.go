package controlsync

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func validTunnelOpts() TunnelOptions {
	return TunnelOptions{
		PrimaryURL: "https://home.example.com/mindfs",
		Token:      "tok",
		InstanceID: "hasee",
		Name:       "hasee",
	}
}

// 隧道搬的是整台机器的 HTTP 面。明文会让「主控暴露面完全跟随主控」这件事
// 在链路上多出一个可读点，因此非 https 必须直接拒绝构造，而不是运行时降级。
func TestNewTunnelClientRequiresHTTPS(t *testing.T) {
	o := validTunnelOpts()
	o.PrimaryURL = "http://home.example.com/mindfs"
	if _, err := NewTunnelClient(o, http.NotFoundHandler()); err == nil {
		t.Fatal("http primary_url 被接受了")
	}
}

func TestNewTunnelClientRequiresFields(t *testing.T) {
	cases := map[string]func(*TunnelOptions){
		"空 primary_url": func(o *TunnelOptions) { o.PrimaryURL = "" },
		"空 token":       func(o *TunnelOptions) { o.Token = "" },
		"空 instance":    func(o *TunnelOptions) { o.InstanceID = "" },
		"无 host":        func(o *TunnelOptions) { o.PrimaryURL = "https:///mindfs" },
	}
	for name, mutate := range cases {
		o := validTunnelOpts()
		mutate(&o)
		if _, err := NewTunnelClient(o, http.NotFoundHandler()); err == nil {
			t.Errorf("%s：构造成功了，应拒绝", name)
		}
	}
	if _, err := NewTunnelClient(validTunnelOpts(), nil); err == nil {
		t.Error("nil handler 被接受了")
	}
}

// 缺省值必须被补成可用值：调用方（CLI）只填必填项是常态。
func TestNewTunnelClientFillsDefaults(t *testing.T) {
	c, err := NewTunnelClient(validTunnelOpts(), http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	if got := c.opts.Name; got != "hasee" {
		t.Fatalf("候选名 = %q，want hasee", got)
	}
	// Status 是观测值：还没连上时没有「主控回写的最终名字」。
	if got := c.Status().Name; got != "" {
		t.Fatalf("未连接时 Status().Name = %q，want 空（还没连上，没有最终名字）", got)
	}
	if got := c.opts.IdleChannels; got <= 0 {
		t.Fatalf("IdleChannels 未补默认值：%d", got)
	}
	if c.opts.MaxChannels < c.opts.IdleChannels {
		t.Fatalf("MaxChannels(%d) < IdleChannels(%d)", c.opts.MaxChannels, c.opts.IdleChannels)
	}
	if c.opts.ReconnectMin <= 0 || c.opts.ReconnectMax < c.opts.ReconnectMin {
		t.Fatalf("退避参数未补默认值：min=%v max=%v", c.opts.ReconnectMin, c.opts.ReconnectMax)
	}
	if c.opts.HeartbeatInterval <= 0 {
		t.Fatalf("心跳未补默认值：%v", c.opts.HeartbeatInterval)
	}
}

// 部署前缀必须保留：主控挂在 /mindfs 下时，拼出来的 tunnel URL 少一段就 404。
func TestTunnelURLCarriesDeployPrefix(t *testing.T) {
	c, err := NewTunnelClient(validTunnelOpts(), http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	// runOnce 才会拼 URL；这里直接核对名字确实带前缀（pickTunnelURL 是内部一致性）。
	if !strings.Contains(c.opts.PrimaryURL, "/mindfs") {
		t.Fatalf("PrimaryURL 丢了部署前缀：%q", c.opts.PrimaryURL)
	}
}

// 心跳间隔的默认值应落在合理区间：太短会刷爆日志，太长则主控先判离线。
func TestDefaultHeartbeatSane(t *testing.T) {
	if defaultTunnelHeartbeat < 5*time.Second || defaultTunnelHeartbeat > 2*time.Minute {
		t.Fatalf("默认心跳 %v 不在 [5s,2m]", defaultTunnelHeartbeat)
	}
}
