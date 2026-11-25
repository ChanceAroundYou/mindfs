package api

import (
	"context"
	"testing"
	"time"
)

// 「会话已完成却仍显示运行中」的空闲看门狗（2026-11-25）。
//
// 行为判据：回合期间长时间没有任何 OnUpdate → 取消回合 ctx（Prompt 由 SDK 的 ctx
// 分支唤醒并给 agent 发 cancel）；有更新就不许取消；正常收尾后看门狗必须退出。

func TestSessionTurnWatchdogCancelsStalledTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := newTurnUpdateTracker()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		watchSessionTurnIdle(ctx, cancel, tracker, 20*time.Millisecond, 5*time.Millisecond, "root", "key", "req", done)
		close(finished)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("空闲超过阈值后回合 ctx 没有被取消")
	}
	<-finished
}

func TestSessionTurnWatchdogKeepsActiveTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := newTurnUpdateTracker()
	done := make(chan struct{})
	go watchSessionTurnIdle(ctx, cancel, tracker, 200*time.Millisecond, 5*time.Millisecond, "root", "key", "req", done)

	// 持续有更新：每次都在阈值内刷新 lastActivity。
	for i := 0; i < 20; i++ {
		tracker.Begin()
		tracker.End()
		time.Sleep(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Fatal("活跃回合被看门狗误杀")
		default:
		}
	}
	close(done)
}

func TestSessionTurnWatchdogStopsWhenTurnEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := newTurnUpdateTracker()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		watchSessionTurnIdle(ctx, cancel, tracker, time.Hour, 5*time.Millisecond, "root", "key", "req", done)
		close(finished)
	}()
	close(done)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("回合收尾后看门狗没有退出")
	}
	if ctx.Err() != nil {
		t.Fatal("正常收尾不得取消回合 ctx")
	}
}
