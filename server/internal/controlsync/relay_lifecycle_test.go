package controlsync

// 数据通道生命周期的三个不变量。它们全是「只在运行期才暴露」的那一类：
// 单次请求永远成功，池子在若干次请求之后悄悄失效，而日志里什么都没有。
//
// 端到端（api/relay_e2e_test.go）能抓到症状，但抓到时不告诉你是哪一条破了。
// 这里把三件事各自钉住。

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// 一次性 listener 的第二次 Accept 必须立刻返回错误。
//
// 如果它阻塞等 Close，而 Close 只由 Serve 返回后才触发，两边互等：
// srv.Serve 永不返回 → 通道永不归还 → 池子被逐条吃空 → 第 N+1 个请求起
// 永久 502。这个死锁在单次请求的测试里完全不可见。
func TestOneShotListenerSecondAcceptReturnsImmediately(t *testing.T) {
	l := &oneShotListener{conn: &fakeConn{}}
	if _, err := l.Accept(); err != nil {
		t.Fatalf("第一次 Accept: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("第二次 Accept 返回了 nil 连接")
		}
	case <-time.After(time.Second):
		t.Fatal("第二次 Accept 阻塞了 —— srv.Serve 将永不返回，数据通道不会被补充")
	}
}

// closeSignalConn 把「底层连接被关」翻译成一个可等待的信号。
//
// serveOneChannel 靠它等请求真正结束：srv.Serve 只负责把连接交出去，
// 它返回时响应可能还没写完，此时关掉隧道会截断响应。
func TestCloseSignalConnSignalsOnClose(t *testing.T) {
	inner := &fakeConn{}
	c := &closeSignalConn{Conn: inner, done: make(chan struct{})}
	select {
	case <-c.done:
		t.Fatal("还没关就发了信号")
	default:
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("关掉连接后 done 没有关闭 —— serveOneChannel 会一直等下去")
	}
	if err := c.Close(); err != nil { // 二次关闭不能 panic
		t.Fatalf("二次 Close: %v", err)
	}
}

// 池子里的 worker 数取 min(IdleChannels, MaxChannels)：一条通道同一时刻只服务
// 一个请求，拨多于 MaxChannels 就绕过了上限。
func TestChannelPoolWorkerCountRespectsMax(t *testing.T) {
	for _, tc := range []struct {
		idle, max, want int
	}{
		{idle: 8, max: 32, want: 8},
		{idle: 8, max: 4, want: 4},
		{idle: 1, max: 1, want: 1},
	} {
		got := channelWorkers(tc.idle, tc.max)
		if got != tc.want {
			t.Fatalf("channelWorkers(%d,%d) = %d，want %d", tc.idle, tc.max, got, tc.want)
		}
	}
}

// 拨通道失败时必须退避，不能热循环：主控临时不可达时，热循环会在一秒内打出
// 上千次带签名的握手。
func TestServeOneChannelBacksOffOnDialFailure(t *testing.T) {
	// 指向一个必然拒绝的地址（TCP 端口 1），并把退避设为 200ms。
	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL:   "https://127.0.0.1:1",
		Token:        "tunnel-test-token-0123456789",
		InstanceID:   "hasee",
		ReconnectMin: 200 * time.Millisecond,
	}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	start := time.Now()
	c.serveOneChannel(context.Background())
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("拨号失败后只过了 %v，没有退避", elapsed)
	}
}
