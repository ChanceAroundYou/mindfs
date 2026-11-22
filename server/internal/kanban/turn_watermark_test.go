package kanban

import (
	"context"
	"testing"
	"time"
)

// W3-1：会话回合的输出水位。
//
// 它回答的问题是「这一轮真的还在吐内容吗」。会话探针只能说「harness 还在回包」，
// 挂死的回合探针照样报在跑；水位是唯一能区分「慢」与「卡住」的证据。

func TestTurnWatermarkAdvancesOnOutputFrames(t *testing.T) {
	svc, root := newTestService(t, nil)
	ctx := context.Background()
	first := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	if _, ok := svc.SessionTurnWatermark(root.ID, "sess-a"); ok {
		t.Fatal("watermark known before any frame")
	}
	svc.NoteSessionTurnOutput(ctx, root.ID, "sess-a", first)
	got, ok := svc.SessionTurnWatermark(root.ID, "sess-a")
	if !ok {
		t.Fatal("watermark unknown right after a frame")
	}
	if !got.Equal(first) {
		t.Fatalf("watermark = %v, want %v", got, first)
	}

	later := first.Add(2 * time.Minute)
	svc.NoteSessionTurnOutput(ctx, root.ID, "sess-a", later)
	got, ok = svc.SessionTurnWatermark(root.ID, "sess-a")
	if !ok || !got.Equal(later) {
		t.Fatalf("watermark after second frame = %v (known=%v), want %v", got, ok, later)
	}

	// 水位按会话隔离：别的会话的帧不能算这一轮的产出。
	if _, ok := svc.SessionTurnWatermark(root.ID, "sess-b"); ok {
		t.Fatal("watermark leaked across sessions")
	}
}

func TestTurnWatermarkUnknownWhenProcessHasNotSeenFrames(t *testing.T) {
	svc, root := newTestService(t, nil)
	ctx := context.Background()
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	seedTask(t, store, "task-wm", StatusRunning)
	// 库里躺着重启前那个进程写的旧水位：它证明「有过产出」，但不能证明「现在还在产出」。
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO task_activities (id, task_id, root_id, type, role, status, session_key, created_at, updated_at, last_output_at)
VALUES ('act-old', 'task-wm', ?, 'agent_reply', 'assistant', ?, 'sess-old', '2026-11-22T08:00:00Z', '2026-11-22T08:00:00Z', '2026-11-22T08:00:00Z')`,
		root.ID, ActivityStatusRunning); err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	if got, ok := svc.SessionTurnWatermark(root.ID, "sess-old"); ok {
		t.Fatalf("watermark = %v, want unknown: a restart must not turn a stale stamp into a stall verdict", got)
	}
}

func TestTurnWatermarkClearedWhenTurnSettles(t *testing.T) {
	svc, root := newTestService(t, nil)
	ctx := context.Background()
	svc.NoteSessionTurnOutput(ctx, root.ID, "sess-c", time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC))
	if _, ok := svc.SessionTurnWatermark(root.ID, "sess-c"); !ok {
		t.Fatal("watermark unknown before clear")
	}
	svc.ClearSessionTurnWatermark(root.ID, "sess-c")
	if _, ok := svc.SessionTurnWatermark(root.ID, "sess-c"); ok {
		t.Fatal("watermark survived turn settle")
	}
}

func TestTurnWatermarkFlushesToReplyAndNodeRunRows(t *testing.T) {
	svc, root := newTestService(t, nil)
	ctx := context.Background()
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	seedTask(t, store, "task-flush", StatusRunning)
	stamp := time.Date(2026, 11, 22, 11, 30, 0, 0, time.UTC)
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO task_activities (id, task_id, root_id, type, role, status, session_key, created_at, updated_at)
VALUES ('act-live', 'task-flush', ?, 'agent_reply', 'assistant', ?, 'sess-flush', '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`,
		root.ID, ActivityStatusRunning); err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_runs (id, task_id, graph_json, created_at, updated_at)
VALUES ('run-flush', 'task-flush', '{}', '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_node_runs (id, run_id, node_id, session_key, status, created_at, updated_at)
VALUES ('nr-flush', 'run-flush', 'agent1', 'sess-flush', ?, '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`,
		NodeRunRunning); err != nil {
		t.Fatalf("insert node run: %v", err)
	}

	svc.NoteSessionTurnOutput(ctx, root.ID, "sess-flush", stamp)

	var activityStamp, nodeStamp string
	if err := store.db.QueryRowContext(ctx,
		`SELECT last_output_at FROM task_activities WHERE id = 'act-live'`).Scan(&activityStamp); err != nil {
		t.Fatalf("read activity stamp: %v", err)
	}
	if err := store.db.QueryRowContext(ctx,
		`SELECT last_output_at FROM workflow_node_runs WHERE id = 'nr-flush'`).Scan(&nodeStamp); err != nil {
		t.Fatalf("read node run stamp: %v", err)
	}
	want := stamp.Format(time.RFC3339Nano)
	if activityStamp != want {
		t.Fatalf("activity last_output_at = %q, want %q", activityStamp, want)
	}
	if nodeStamp != want {
		t.Fatalf("node run last_output_at = %q, want %q", nodeStamp, want)
	}
}
