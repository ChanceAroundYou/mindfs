package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeEventCLI writes a script that prints one line per run (stdout or
// stderr, depending on the line) and then exits. A run counter lets a test
// see restarts without waiting on wall-clock guesses.
func fakeEventCLI(t *testing.T, line string, toStderr bool) (bin, counterPath string) {
	t.Helper()
	dir := t.TempDir()
	counterPath = filepath.Join(dir, "runs.txt")
	bin = filepath.Join(dir, "lark-cli")
	target := ">&1"
	if toStderr {
		target = ">&2"
	}
	script := "#!/bin/sh\n" +
		"echo run >> " + counterPath + "\n" +
		"cat <<'LINE' " + target + "\n" + line + "\nLINE\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, counterPath
}

func runCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(raw)))
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestSupervisorDispatchesClickAndRestartsAfterExit(t *testing.T) {
	bin, counter := fakeEventCLI(t, `{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_1","action_value":"{\"card\":\"gate-1\",\"choice\":0}"}`, false)
	seen := make(chan Action, 4)
	sup := &Supervisor{Path: bin, RestartDelay: 10 * time.Millisecond, ConflictDelay: 10 * time.Millisecond,
		Logf:   func(string, ...any) {},
		Handle: func(_ context.Context, a Action) error { seen <- a; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	select {
	case a := <-seen:
		if a.CardID != "gate-1" || a.Choice != 0 || a.OperatorID != "ou_owner" {
			t.Fatalf("action = %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("click not dispatched")
	}
	waitFor(t, 2*time.Second, func() bool { return runCount(counter) >= 2 })
}

func TestSupervisorIgnoresNonClickLinesAndSurvivesHandlerErrors(t *testing.T) {
	bin, _ := fakeEventCLI(t, `{"type":"im.message.receive_v1"}`, false)
	var calls atomic.Int32
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 5 * time.Millisecond,
		Logf:   func(string, ...any) {},
		Handle: func(context.Context, Action) error { calls.Add(1); return fmt.Errorf("boom") }}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	sup.Run(ctx)
	if calls.Load() != 0 {
		t.Fatalf("handler called %d times for a non-click line", calls.Load())
	}
}

func TestSupervisorWaitsLongerOnConflictAndStopsOnContext(t *testing.T) {
	bin, counter := fakeEventCLI(t, "another event bus is already connected", true)
	var logged strings.Builder
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 80 * time.Millisecond,
		Logf:   func(format string, args ...any) { logged.WriteString(fmt.Sprintf(format, args...)) },
		Handle: func(context.Context, Action) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	sup.Run(ctx)
	if runs := runCount(counter); runs != 2 {
		t.Fatalf("runs = %d, want 2 (conflict delay must dominate the 5ms restart delay)", runs)
	}
	if got := strings.Count(logged.String(), "拒绝再连"); got != 1 {
		t.Fatalf("conflict logged %d times, want once per episode:\n%s", got, logged.String())
	}
}

func TestSupervisorStopsWhenContextIsCanceled(t *testing.T) {
	bin, counter := fakeEventCLI(t, "noise", false)
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 5 * time.Millisecond,
		Logf: func(string, ...any) {}, Handle: func(context.Context, Action) error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	waitFor(t, 2*time.Second, func() bool { return runCount(counter) >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// 一行超过扫描上限（1 MiB）会让 scanner 停下来，但子进程还活着、管道还开着：
// 不主动收掉它，cmd.Wait() 就永远不返回 —— 监听就此静默死亡，点击再也收不到。
// 正确行为是记一笔并重建连接。
func TestSupervisorReconnectsWhenAStdoutLineIsTooLong(t *testing.T) {
	dir := t.TempDir()
	counterPath := filepath.Join(dir, "runs.txt")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"echo run >> " + counterPath + "\n" +
		"head -c 1100000 /dev/zero | tr '\\0' 'x'\n" +
		"echo\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := &Supervisor{Path: bin, Handle: func(context.Context, Action) error { return nil },
		Logf: noLog, RestartDelay: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	waitFor(t, 10*time.Second, func() bool { return runCount(counterPath) >= 2 })
}

// lark-cli 把 stdin 的 EOF 当停机信号（`consume treats stdin EOF as exit
// signal`）。exec 默认给子进程 /dev/null，监听会在一秒多就自己退出，
// supervisor 于是永远在「起 → 退 → 重连」里空转，点击一条都收不到。
func TestSupervisorKeepsListenerStdinOpen(t *testing.T) {
	dir := t.TempDir()
	counterPath := filepath.Join(dir, "runs.txt")
	bin := filepath.Join(dir, "lark-cli")
	// 和真 lark-cli 一样：stdin 一 EOF 就退出；stdin 开着就一直挂住。
	script := "#!/bin/sh\n" +
		"echo run >> " + counterPath + "\n" +
		"cat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := &Supervisor{Path: bin, Handle: func(context.Context, Action) error { return nil },
		Logf: noLog, RestartDelay: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	waitFor(t, 2*time.Second, func() bool { return runCount(counterPath) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if runs := runCount(counterPath); runs != 1 {
		t.Fatalf("监听被重启了 %d 次：子进程的 stdin 立刻 EOF，lark-cli 会自己退出", runs)
	}
}

// 收子进程必须走 SIGTERM：lark-cli 警告 kill -9 会跳过清理、泄漏飞书
// 服务端的订阅，重启后几小时内都报「another event bus is already
// connected」（supervisor 里那段 60 秒退避就是为它写的）。
func TestSupervisorStopsListenerWithSIGTERM(t *testing.T) {
	dir := t.TempDir()
	startedPath := filepath.Join(dir, "started.txt")
	termPath := filepath.Join(dir, "term.txt")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"trap 'echo term > " + termPath + "; exit 0' TERM\n" +
		"echo started > " + startedPath + "\n" +
		"while :; do sleep 0.02; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := &Supervisor{Path: bin, Handle: func(context.Context, Action) error { return nil },
		Logf: noLog, RestartDelay: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	})
	cancel()
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(termPath)
		return err == nil
	})
}

// 一个卡住的处理器不能把重连一起拖死：lark-cli 退出后 consume 要等
// handlers.Wait()，没有超时的话监听就再也不回来了。
func TestSupervisorHandlerTimeoutDoesNotBlockReconnect(t *testing.T) {
	event := `{"type":"card.action.trigger","event_id":"e1","operator_id":"ou_owner","message_id":"om_1","action_value":"{\"card\":\"gate-nr-1\",\"choice\":0}"}`
	bin, counterPath := fakeEventCLI(t, event, false)
	sup := &Supervisor{Path: bin,
		// 处理器等的是自己的 ctx：没有每次点击的超时，它拿到的是监听进程那条
		// 长寿 ctx，能一直把 handlers.Wait() 拖住。
		Handle: func(ctx context.Context, _ Action) error { <-ctx.Done(); return ctx.Err() },
		Logf:   noLog, RestartDelay: 10 * time.Millisecond, HandlerTimeout: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	waitFor(t, 10*time.Second, func() bool { return runCount(counterPath) >= 3 })
}

// 第二条长连接只收私聊文本事件。它必须有自己的事件类型、必须把每行原文
// 直接交给 HandleRaw，而且绝不能落到卡片点击的 Handle 上。
func TestSupervisorUsesConfiguredEventTypeAndRawHandler(t *testing.T) {
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv.txt")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + argvPath + "\n" +
		"cat <<'LINE'\n" + strings.TrimSpace(string(line)) + "\nLINE\n" +
		"cat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	raw := make(chan []byte, 4)
	var clicks atomic.Int32
	sup := &Supervisor{Path: bin, EventType: "im.message.receive_v1", Logf: noLog,
		HandleRaw: func(_ context.Context, b []byte) error { raw <- append([]byte(nil), b...); return nil },
		Handle:    func(context.Context, Action) error { clicks.Add(1); return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	select {
	case got := <-raw:
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(line)) {
			t.Fatalf("raw line = %q, want the fixture verbatim", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("raw handler never saw the message; argv:\n%s", readText(t, argvPath))
	}
	waitFor(t, 5*time.Second, func() bool {
		argv, err := os.ReadFile(argvPath)
		return err == nil && strings.Contains(string(argv), "event consume im.message.receive_v1 --as bot")
	})
	if n := clicks.Load(); n != 0 {
		t.Fatalf("card handler called %d times for a chat message", n)
	}
}

// Service 必须同时拉起两条连接，并把私聊原文送到注入的入站处理器；缺一条
// 时消息或卡片会静默消失，而 Wait 也必须两条都等到。
func TestServiceStartRunsBothListenersAndForwardsRawMessage(t *testing.T) {
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv.txt")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"args=\"$*\"\n" +
		"printf '%s\\n' \"$args\" >> " + argvPath + "\n" +
		"case \"$args\" in\n" +
		"  *im.message.receive_v1*) cat <<'LINE'\n" + strings.TrimSpace(string(line)) + "\nLINE\n  ;;\n" +
		"esac\n" +
		"cat >/dev/null\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	st := NewStore(filepath.Join(dir, "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	svc := New(Config{OwnerOpenID: "ou_owner", LarkCLIPath: bin, Logf: noLog,
		DeliverInterval: time.Hour}, st, &CLI{Path: bin, Timeout: 2 * time.Second})
	raw := make(chan []byte, 4)
	svc.SetMessageHandler(func(_ context.Context, b []byte) error {
		raw <- append([]byte(nil), b...)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	defer func() { cancel(); svc.Wait() }()

	select {
	case got := <-raw:
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(line)) {
			t.Fatalf("service raw line = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("service never forwarded the message; argv:\n%s", readText(t, argvPath))
	}
	waitFor(t, 5*time.Second, func() bool {
		argv, err := os.ReadFile(argvPath)
		if err != nil {
			return false
		}
		text := string(argv)
		return strings.Contains(text, "event consume card.action.trigger --as bot") &&
			strings.Contains(text, "event consume im.message.receive_v1 --as bot")
	})
}
