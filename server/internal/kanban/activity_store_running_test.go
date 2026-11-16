package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Workflow 的启动与结算都在异步路径上：结算可能先落库，随后才轮到「标记
// running」的 goroutine。running 是未结算状态，不能把已经 completed/failed 的
// 回复覆盖回去，否则重连后看到的就是一条永远转圈的回复。
func TestActivityStore_MarkRunningIfActiveKeepsTerminalStatus(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for id, status := range map[string]string{
		"pending":   ActivityStatusPending,
		"completed": ActivityStatusCompleted,
	} {
		if err := store.Insert(ctx, &TaskActivity{
			ID: id, TaskID: "task-1", Type: ActivityTypeAgentReply, Role: RoleAgent,
			Body: "reply", Status: status, CreatedAt: base,
		}); err != nil {
			t.Fatalf("Insert(%s): %v", id, err)
		}
	}

	if started, err := store.MarkRunningIfActive(ctx, "pending"); err != nil || !started {
		t.Fatalf("MarkRunningIfActive(pending) = %v, %v; want true, nil", started, err)
	}
	if got, _ := store.Get(ctx, "pending"); got.Status != ActivityStatusRunning {
		t.Fatalf("pending status = %q, want running", got.Status)
	}

	if started, err := store.MarkRunningIfActive(ctx, "completed"); err != nil || started {
		t.Fatalf("MarkRunningIfActive(completed) = %v, %v; want false, nil", started, err)
	}
	if got, _ := store.Get(ctx, "completed"); got.Status != ActivityStatusCompleted {
		t.Fatalf("completed status = %q, want completed", got.Status)
	}

	if _, err := store.MarkRunningIfActive(ctx, "missing"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("MarkRunningIfActive(missing) err = %v, want ErrActivityNotFound", err)
	}
}
