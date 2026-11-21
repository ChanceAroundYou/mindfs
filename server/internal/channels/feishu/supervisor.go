package feishu

import (
	"bufio"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Supervisor keeps `lark-cli event consume card.action.trigger` running and
// hands every click to Handle. Feishu allows one listener per app, so a
// refused connection means another process still holds it — that is worth a
// minute between tries, and worth saying once rather than every minute.
type Supervisor struct {
	Path          string
	Handle        func(context.Context, Action) error
	Logf          func(string, ...any)
	RestartDelay  time.Duration
	ConflictDelay time.Duration
	// HandlerTimeout bounds one click's handling. Without it a handler stuck
	// on a slow store call holds handlers.Wait() — and therefore the whole
	// reconnect loop — open for as long as it likes.
	HandlerTimeout time.Duration
}

const conflictMarker = "another event bus is already connected"

// Run blocks until ctx is canceled, restarting the listener whenever it exits.
func (s *Supervisor) Run(ctx context.Context) {
	logf := s.Logf
	if logf == nil {
		logf = log.Printf
	}
	restart := s.RestartDelay
	if restart <= 0 {
		restart = 10 * time.Second
	}
	conflictDelay := s.ConflictDelay
	if conflictDelay <= 0 {
		conflictDelay = 60 * time.Second
	}

	refused := false
	for ctx.Err() == nil {
		conflict := s.consume(ctx, logf)
		if ctx.Err() != nil {
			return
		}
		if conflict && !refused {
			logf("飞书那边还记着这个应用的另一条事件连接，拒绝再连：在它放开之前收不到卡片点击。" +
				"常见于刚重启，旧连接可能要几个小时才放开；别的电脑也在跑这个应用的监听就先停掉它。" +
				"1 分钟后再试，连上前不再重复这条。")
		}
		refused = conflict
		delay := restart
		if conflict {
			delay = conflictDelay
		} else {
			logf("飞书卡片监听退出了，%s 后重连", delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// consume runs one listener process and reports whether it hit the
// single-listener conflict. It returns when the process exits.
func (s *Supervisor) consume(ctx context.Context, logf func(string, ...any)) bool {
	// 子进程要能在读端放弃时被收掉：scanner 停下来（比如一行超过 1 MiB）之后
	// 仍握着管道，cmd.Wait() 会一直等下去，监听就静默死掉了。
	childCtx, stopChild := context.WithCancel(ctx)
	defer stopChild()
	cmd := exec.CommandContext(childCtx, s.Path, "event", "consume", "card.action.trigger", "--as", "bot")
	// lark-cli 把 stdin EOF 当停机信号；exec 默认给子进程 /dev/null，
	// 监听会在一秒多就自己退掉。留一根一直开着的管道给它。
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		logf("飞书卡片监听起不来：%v", err)
		return false
	}
	cmd.Stdin = stdinR
	// lark-cli 明确要求 SIGTERM：kill -9 会跳过清理、泄漏服务端的订阅，
	// 重启后几小时内都连不上（就是上面那段 60 秒退避在等的冲突）。
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		logf("飞书卡片监听起不来：%v", err)
		return false
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		logf("飞书卡片监听起不来：%v", err)
		return false
	}
	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		logf("飞书卡片监听起不来（%s）：%v", s.Path, err)
		return false
	}
	stdinR.Close()

	var conflict bool
	var mu sync.Mutex
	var handlers sync.WaitGroup
	timeout := s.HandlerTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, conflictMarker) {
				mu.Lock()
				conflict = true
				mu.Unlock()
			}
			logf("lark-cli: %s", line)
		}
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		ev, ok := ParseEvent(line)
		if !ok {
			continue
		}
		action, ok := ev.ParseAction()
		if !ok {
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			hctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := s.Handle(hctx, action); err != nil {
				logf("卡片点击没处理成（%s）：%v", action.CardID, err)
			}
		}()
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		// 读不到更多事件了，进程却还在：主动收掉，让外层重建连接。
		logf("飞书事件流读取中断：%v", err)
		stopChild()
	}

	waitErr := cmd.Wait()
	stdinW.Close()
	handlers.Wait()
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) && ctx.Err() == nil {
		logf("飞书卡片监听退出：%v", waitErr)
	}
	mu.Lock()
	defer mu.Unlock()
	return conflict
}
