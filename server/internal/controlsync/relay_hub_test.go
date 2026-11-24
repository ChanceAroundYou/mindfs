package controlsync

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// fakeConn 是只用来占位的 net.Conn：hub 只关心「有没有一条连接」，
// 不做读写。Close 会被记录，用来验证池子清空确实关掉了空闲通道。
type fakeConn struct {
	closed bool
}

func (c *fakeConn) Read([]byte) (int, error)         { return 0, errors.New("not implemented") }
func (c *fakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *fakeConn) Close() error                     { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

// 没有登记隧道时必须是 offline，而不是「等一会儿」—— 前者是「这台机器关机了」，
// 后者是「它跟不上」。前端对两者的处理不同（前者标红并给出地址，后者提示重试）。
func TestRelayHubAcquireUnknownEdgeIsOffline(t *testing.T) {
	h := NewRelayHub()
	_, err := h.Acquire(context.Background(), "hasee")
	if !errors.Is(err, ErrRelayOffline) {
		t.Fatalf("err = %v，want ErrRelayOffline", err)
	}
}

// 隧道在、但池子里一条空闲通道都没有 → busy（不是 offline）。这条区分决定了
// 前端文案：offline 说「机器不在线」，busy 说「连接被占满」。
func TestRelayHubAcquireEmptyPoolIsBusy(t *testing.T) {
	h := NewRelayHub()
	if _, err := h.Register("hasee", "hasee", 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := h.Acquire(ctx, "hasee")
	if !errors.Is(err, ErrRelayBusy) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v，want ErrRelayBusy 或 ctx 超时", err)
	}
}

// 借出去的通道必须就是交进来的那一条：relay 的一条数据通道只服务一次请求，
// 中间若被换掉，请求会跑到一个没人回应的连接上。
func TestRelayHubAttachThenAcquireHandsOverSameConn(t *testing.T) {
	h := NewRelayHub()
	if _, err := h.Register("hasee", "hasee", 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	conn := &fakeConn{}
	if !h.Attach("hasee", conn) {
		t.Fatal("attach 返回 false，池子明明有空位")
	}
	got, err := h.Acquire(context.Background(), "hasee")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got != conn {
		t.Fatalf("借到的不是交进去的那条连接")
	}
	// 借走之后池子空了：再来一次应该等不到（busy）。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Acquire(ctx, "hasee"); err == nil {
		t.Fatal("同一条通道被借了两次")
	}
}

// 池满时 Attach 必须返回 false，让调用方关掉多余的那条 —— 否则边控端会
// 一直白拨连接，而主控这边静默丢弃。
func TestRelayHubAttachRejectsWhenPoolFull(t *testing.T) {
	h := NewRelayHub()
	if _, err := h.Register("hasee", "hasee", 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !h.Attach("hasee", &fakeConn{}) {
		t.Fatal("第一条通道应被收下")
	}
	if h.Attach("hasee", &fakeConn{}) {
		t.Fatal("池子只有 1 个位置，第二条应被拒绝")
	}
}

// 重连时 Register 替换旧隧道，旧池里的空闲通道必须被关掉：
// 对端已经不读它们了，留着只会让请求被派到死连接上。
func TestRelayHubRegisterReplacesAndClosesOldPool(t *testing.T) {
	h := NewRelayHub()
	if _, err := h.Register("hasee", "hasee", 2); err != nil {
		t.Fatalf("register: %v", err)
	}
	old := &fakeConn{}
	h.Attach("hasee", old)

	if _, err := h.Register("hasee", "hasee", 2); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if !old.closed {
		t.Fatal("旧池里的空闲通道没被关掉")
	}
	// 新池是空的：旧连接不该被算作新隧道的容量。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Acquire(ctx, "hasee"); err == nil {
		t.Fatal("重连后借到了旧隧道的连接")
	}
}

// Unregister 只清自己那条：旧连接延迟清理时，不能把重连后的新隧道一起带走。
func TestRelayHubUnregisterIgnoresStaleEdge(t *testing.T) {
	h := NewRelayHub()
	old, err := h.Register("hasee", "hasee", 1)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := h.Register("hasee", "hasee", 1); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	h.Unregister(old)
	if !h.Online("hasee") {
		t.Fatal("旧连接的延迟清理把新隧道一起注销了")
	}
}

func TestRelayHubUnregisterRemovesLiveEdge(t *testing.T) {
	h := NewRelayHub()
	edge, err := h.Register("hasee", "hasee", 1)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	conn := &fakeConn{}
	h.Attach("hasee", conn)
	h.Unregister(edge)
	if h.Online("hasee") {
		t.Fatal("注销后仍报告在线")
	}
	if !conn.closed {
		t.Fatal("注销时没有关掉空闲通道")
	}
	if _, err := h.Acquire(context.Background(), "hasee"); !errors.Is(err, ErrRelayOffline) {
		t.Fatalf("err = %v，want ErrRelayOffline", err)
	}
}

// 没登记就 Attach 必须失败：否则一条数据通道会挂在无处可用的池子里。
func TestRelayHubAttachBeforeRegisterFails(t *testing.T) {
	h := NewRelayHub()
	if h.Attach("hasee", &fakeConn{}) {
		t.Fatal("没有控制通道时不该收下数据通道")
	}
}

func TestRelayHubOnlineNamesSorted(t *testing.T) {
	h := NewRelayHub()
	for _, name := range []string{"zebra", "alpha"} {
		if _, err := h.Register(name, name, 1); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	names := h.OnlineNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zebra" {
		t.Fatalf("names = %v，want [alpha zebra]", names)
	}
}

// ---- 名字规则 ----

// 名字会出现在 URL 路径的一段里。点、大写、下划线、中文都不能放行：
// 点会和 ".." 混淆，大写会让大小写敏感/不敏感两种解析打起来。
func TestValidRelayName(t *testing.T) {
	valid := []string{"hasee", "pc-win", "a", "edge-01", "relay-8f3a1c2b"}
	for _, n := range valid {
		if !ValidRelayName(n) {
			t.Errorf("ValidRelayName(%q) = false，应为 true", n)
		}
	}
	invalid := []string{"", "-", "a-", "-a", "Hasee", "ha_see", "ha.see", "hasee/", "中文",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} // 33 字符
	for _, n := range invalid {
		if ValidRelayName(n) {
			t.Errorf("ValidRelayName(%q) = true，应为 false", n)
		}
	}
}

func TestSlugRelayName(t *testing.T) {
	cases := map[string]string{
		"hasee":            "hasee",
		"PC-HOME":          "pc-home",
		"pc_home.local":    "pc-home-local",
		"  Hasee Office  ": "hasee-office",
		"---":              "",
		"中文":               "",
		"a--b":             "a-b",
		"x":                "x",
	}
	for in, want := range cases {
		if got := SlugRelayName(in); got != want {
			t.Errorf("SlugRelayName(%q) = %q，want %q", in, got, want)
		}
	}
}

// 名字分配的四条规则，顺序不能换。
func TestAssignRelayName(t *testing.T) {
	taken := map[string]string{"bob": "other-machine"}

	// 1. 已有名字且没被别人占着 → 原样保留，不管候选名想要什么。
	if got := AssignRelayName("hasee", "wanted", "hasee-id", taken); got != "hasee" {
		t.Errorf("已有名字应保留，got %q", got)
	}
	// 2. 候选名空闲 → 用它（此时没有已有名字）。
	if got := AssignRelayName("", "office-pc", "hasee-id", taken); got != "office-pc" {
		t.Errorf("候选名空闲应用它，got %q", got)
	}
	// 2b. 已有名字被别的机器占着 → 不能抢，回到候选名。
	if got := AssignRelayName("bob", "office-pc", "hasee-id", taken); got != "office-pc" {
		t.Errorf("被占用的已有名字不能抢，got %q", got)
	}
	// 3. 候选名也被占 → 追加 instance 短后缀。
	got := AssignRelayName("", "bob", "hasee-id", taken)
	if got == "bob" || !ValidRelayName(got) {
		t.Errorf("冲突时应加后缀，got %q", got)
	}
	// 4. 候选名非法 → 先 slug，再分配。
	if got := AssignRelayName("", "Office PC", "hasee-id", taken); got != "office-pc" {
		t.Errorf("非法候选名应先 slug，got %q", got)
	}
	// 空候选名 → 兜底 edge。
	if got := AssignRelayName("", "", "hasee-id", taken); got != "edge" {
		t.Errorf("空候选名应兜底 edge，got %q", got)
	}
	// 自己的名字不算冲突（重连必须回到同一个地址）。
	if got := AssignRelayName("bob", "", "other-machine", taken); got != "bob" {
		t.Errorf("自己占着的名字应原样保留，got %q", got)
	}
}

// 同一台机器重复分配必须稳定：地址一旦发出去，其他设备的节点表里就存着它。
func TestAssignRelayNameIsIdempotentForSameInstance(t *testing.T) {
	taken := map[string]string{}
	first := AssignRelayName("", "office-pc", "hasee-id", taken)
	taken[first] = "hasee-id"
	for i := 0; i < 5; i++ {
		if got := AssignRelayName(first, "office-pc", "hasee-id", taken); got != first {
			t.Fatalf("第 %d 次分配漂移了：%q → %q", i, first, got)
		}
	}
}
