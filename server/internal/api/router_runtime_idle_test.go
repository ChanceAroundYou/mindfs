package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 复核回合的判死条件必须是「没有事件」，不是「总时长」。
// 现场（2026-11-25）：复核持续产出工具调用 5 分 43 秒，120 秒硬切把已经
// 算出来的 pass 结论丢掉，界面只剩「正在独立复核」。
func TestIdleWatchdogCancelsAfterSilence(t *testing.T) {
	w := newIdleWatchdog(context.Background(), 40*time.Millisecond)
	defer w.Close()

	select {
	case <-w.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("空闲看门狗没有在静默期后取消回合")
	}
	if !w.TimedOut() {
		t.Fatal("取消原因不是空闲超时，无法与父 ctx 的 deadline 区分")
	}
	if !errors.Is(context.Cause(w.ctx), errRouterTurnIdle) {
		t.Fatalf("cause = %v, want errRouterTurnIdle", context.Cause(w.ctx))
	}
}

// 持续有事件时不能取消：这正是被 120 秒硬切误杀的形态。
func TestIdleWatchdogKeepsAliveWhileTouched(t *testing.T) {
	w := newIdleWatchdog(context.Background(), 120*time.Millisecond)
	defer w.Close()

	for i := 0; i < 20; i++ {
		w.Touch()
		time.Sleep(30 * time.Millisecond)
		if err := w.ctx.Err(); err != nil {
			t.Fatalf("第 %d 次活动后回合已被取消：%v（空闲判据把它误杀了）", i, err)
		}
	}
	if w.TimedOut() {
		t.Fatal("持续有活动却报空闲超时")
	}
}

// 父 ctx 自己的取消不能被误报成空闲超时。
func TestIdleWatchdogParentCancelIsNotIdleTimeout(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	w := newIdleWatchdog(parent, time.Hour)
	defer w.Close()

	cancel()
	<-w.ctx.Done()
	if w.TimedOut() {
		t.Fatal("父 ctx 取消被误判成空闲超时")
	}
}

// 未设置预算（0）时不启用看门狗，保持旧行为。
func TestIdleWatchdogDisabledWhenNoBudget(t *testing.T) {
	if w := newIdleWatchdog(context.Background(), 0); w != nil {
		t.Fatalf("Timeout=0 不应启用看门狗，got %#v", w)
	}
	ctx := context.Background()
	var w *idleWatchdog
	if got := w.CtxOr(ctx); got != ctx {
		t.Fatal("nil 看门狗应回落到原 ctx")
	}
	w.Touch()
	w.Close()
	if w.TimedOut() {
		t.Fatal("nil 看门狗不应报超时")
	}
}
