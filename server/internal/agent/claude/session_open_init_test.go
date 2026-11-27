package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAwaitInitializedReturnsValueAndKeepsCancel(t *testing.T) {
	var openCtx context.Context
	value, cancel, err := awaitInitialized(context.Background(), time.Second,
		func(ctx context.Context) (int, error) {
			openCtx = ctx
			return 42, nil
		}, nil)
	if err != nil {
		t.Fatalf("awaitInitialized err = %v", err)
	}
	if value != 42 {
		t.Fatalf("value = %d, want 42", value)
	}
	if cancel == nil {
		t.Fatal("cancel must be returned on success (Stream lifetime is bound to it)")
	}
	if openCtx == nil || openCtx.Err() != nil {
		t.Fatalf("open context must stay alive on success, err=%v", openCtx.Err())
	}
	cancel()
	select {
	case <-openCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel() did not end the stream context")
	}
}

// 2026-11-27 事故：CLI 提前退出不回答 initialize，SDK 永远等不到响应。
// 上限必须自己兜住，并在超时后回收失败的 CLI（abort）。
func TestAwaitInitializedTimesOutAndAborts(t *testing.T) {
	aborted := make(chan struct{})
	start := time.Now()
	_, _, err := awaitInitialized(context.Background(), 40*time.Millisecond,
		func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
		func() { close(aborted) })
	if err == nil || !strings.Contains(err.Error(), "initializ") {
		t.Fatalf("err = %v, want initialization timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("abort was not called on timeout")
	}
}

// open 自己报错时也要 abort，且返回原始错误（例如 resume 转录不存在）。
func TestAwaitInitializedPropagatesOpenError(t *testing.T) {
	sentinel := errors.New("no conversation found")
	aborted := false
	_, _, err := awaitInitialized(context.Background(), time.Second,
		func(context.Context) (int, error) { return 0, sentinel },
		func() { aborted = true })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if !aborted {
		t.Fatal("abort must be called when open fails")
	}
}

// 调用方已有更短的 deadline 时应提前放弃，不能被固定的初始化上限拖住。
func TestAwaitInitializedHonorsShorterParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := awaitInitialized(ctx, time.Hour,
		func(inner context.Context) (int, error) {
			<-inner.Done()
			return 0, inner.Err()
		}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("parent deadline ignored, took %s", elapsed)
	}
}
