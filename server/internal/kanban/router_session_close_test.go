package kanban

import (
	"context"
	"testing"
	"time"
)

// 空闲关闭是**正常生命周期**，不是错误：它过去把 "idle timeout" 写进
// router_sessions.last_error，排查线上问题时每一行已关闭的会话都像出过错。
func TestIdleSweepClosesSessionWithoutRecordingError(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newDirectReplyRecoveryFixture(t, "task-idle", "sess-idle", StatusWaitingUser)
	routerStore := NewRouterStore(store.db)
	if err := routerStore.UpsertSession(ctx, &RouterSessionRecord{
		Scope: RouterScopeTask, OwnerID: "task-idle", RootID: "root", SessionKey: "router-task-idle",
		Status: RouterSessionActive, LastTurnAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	rs := &RouterService{rootID: "root", store: routerStore, closed: make(chan struct{}), lanes: map[string]*routerLane{}}

	rs.sweepIdleOnce(time.Hour)

	record, err := routerStore.Session(ctx, RouterScopeTask, "task-idle")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if record.Status != RouterSessionClosed {
		t.Fatalf("status = %q, want closed", record.Status)
	}
	if record.LastError != "" {
		t.Fatalf("正常空闲关闭写了 last_error = %q，排查时会被当成故障", record.LastError)
	}
}
