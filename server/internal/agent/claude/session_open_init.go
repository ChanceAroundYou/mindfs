package claude

import (
	"context"
	"fmt"
	"time"
)

// claudeInitTimeout 是 CLI 初始化（含 --resume 载入）的上限。
//
// 正常启动在秒级完成；超过上限只可能是 CLI 卡住或提前退出却没回答 initialize。
// 必须自己兜住：SDK 的 Initialize 只在「收到 control response」或「ctx 取消」时返回，
// 子进程提前退出时既不回响应也不取消，调用方会永久阻塞（2026-11-27 事故）。
// 是变量是为了让回归测试能把上限压到毫秒级。
var claudeInitTimeout = 90 * time.Second

// awaitInitialized 在 timeout 内等待 open 完成，用 abort 强制回收失败的那一侧。
//
// 成功时返回的 cancel 必须由调用方持有到会话结束：open 用的 initCtx 同时是
// Stream 的生命周期上下文，提前 cancel 会让会话静默失效。
func awaitInitialized[T any](
	ctx context.Context,
	timeout time.Duration,
	open func(context.Context) (T, error),
	abort func(),
) (T, context.CancelFunc, error) {
	var zero T
	if timeout <= 0 {
		timeout = claudeInitTimeout
	}
	clampedToParent := false
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
			clampedToParent = true
		}
	}
	initCtx, cancel := context.WithCancel(ctx)
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := open(initCtx)
		done <- result{value: value, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-done:
		if res.err != nil {
			cancel()
			if abort != nil {
				abort()
			}
			return zero, nil, res.err
		}
		return res.value, cancel, nil
	case <-timer.C:
		cancel()
		if abort != nil {
			abort()
		}
		if clampedToParent {
			return zero, nil, context.DeadlineExceeded
		}
		return zero, nil, fmt.Errorf("claude session initialize timed out after %s", timeout)
	}
}
