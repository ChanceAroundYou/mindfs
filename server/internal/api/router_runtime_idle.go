package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errRouterTurnIdle 是空闲看门狗取消回合 ctx 的原因。它和父 ctx 自己的
// DeadlineExceeded 必须能区分：前者说明「模型长时间没有任何事件」，后者说明
// 「整个回合的总预算用完了」。
var errRouterTurnIdle = errors.New("router turn idle timeout")

// idleWatchdog 给一个回合加「无事件才超时」的判据。
//
// 为什么不是硬超时：复核回合要在工作区里读代码核对产出（2026-11-25 实测一次
// 73 次工具调用、5 分 43 秒），120 秒硬切会把仍在持续产出的回合拦腰砍断，
// 结论已经生成却没人收。真正该判死的是「长时间一条事件都没有」——那才是
// 卡住。总上限仍由外层 ctx 的 deadline 负责。
//
// 零值/空闲时长 <= 0 时不启用，返回 nil；所有方法都能安全地在 nil 上调用。
type idleWatchdog struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	touch  chan struct{}
	stop   chan struct{}
	once   sync.Once
}

func newIdleWatchdog(parent context.Context, idle time.Duration) *idleWatchdog {
	if parent == nil || idle <= 0 {
		return nil
	}
	ctx, cancel := context.WithCancelCause(parent)
	w := &idleWatchdog{
		ctx:    ctx,
		cancel: cancel,
		// 容量 1：连续事件只需合并成一次「有活动」，不排队。
		touch: make(chan struct{}, 1),
		stop:  make(chan struct{}),
	}
	go w.run(idle)
	return w
}

func (w *idleWatchdog) run(idle time.Duration) {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-w.ctx.Done():
			return
		case <-w.touch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		case <-timer.C:
			w.cancel(errRouterTurnIdle)
			return
		}
	}
}

// Touch 标记「刚刚有事件」。可由事件回调并发调用。
func (w *idleWatchdog) Touch() {
	if w == nil {
		return
	}
	select {
	case w.touch <- struct{}{}:
	default:
	}
}

// TimedOut 报告 ctx 是否因空闲超时被取消（而不是父 ctx 的 deadline/取消）。
func (w *idleWatchdog) TimedOut() bool {
	return w != nil && errors.Is(context.Cause(w.ctx), errRouterTurnIdle)
}

// CtxOr 返回看门狗 ctx；未启用时回落到入参。
func (w *idleWatchdog) CtxOr(fallback context.Context) context.Context {
	if w == nil {
		return fallback
	}
	return w.ctx
}

// Close 停掉看门狗并释放 ctx。可重复调用。
func (w *idleWatchdog) Close() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		close(w.stop)
		w.cancel(nil)
	})
}

// idleTurnError 把 SendMessage 的返回错误翻译成可读原因：空闲超时不能被当成
// 一次普通的 context 取消，日志里要看得出是「没有事件」而不是「总预算用完」。
func idleTurnError(w *idleWatchdog, idle time.Duration, err error) error {
	if err == nil || !w.TimedOut() {
		return err
	}
	return fmt.Errorf("回合空闲超时：连续 %s 没有任何新事件（%w）", idle, err)
}
