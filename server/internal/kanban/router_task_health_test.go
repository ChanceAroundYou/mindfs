package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// W3-2：任务级巡检。
//
// 既有的监管只会在**事件**上被唤醒：Run 失败、gate 等待、会话探针空闲停摆。
// 三种现场它都看不见 —— 回合在跑但一个字节都不吐（挂死）、任务写着 running 而
// 底下什么都没了（重启/崩溃留下的孤儿）、waiting_user 却有会话在跑。这三种
// 恰恰是「重启后不会主动恢复」的现场，所以要有一拍一次的巡检。

func TestClassifyTaskHealth(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	runningReply := taskHealthReply{ActivityID: "act-1", SessionKey: "s-1", CreatedAt: now.Add(-30 * time.Minute)}
	freshReply := runningReply
	freshReply.LastOutput, freshReply.OutputKnown = now.Add(-time.Minute), true
	staleReply := runningReply
	staleReply.LastOutput, staleReply.OutputKnown = now.Add(-11*time.Minute), true
	unknownReply := runningReply // 水位未知：刚重启，不能判停滞

	cases := []struct {
		name string
		in   taskHealthInput
		want taskHealth
	}{
		{
			name: "running with stale watermark is a stalled turn even while the probe says replying",
			in: taskHealthInput{Status: StatusRunning, Now: now,
				Replies: []taskHealthReply{staleReply}, SessionLive: true, OpenNodes: true},
			want: taskHealthStalledTurn,
		},
		{
			name: "running with fresh output is healthy",
			in: taskHealthInput{Status: StatusRunning, Now: now,
				Replies: []taskHealthReply{freshReply}, SessionLive: true},
			want: taskHealthHealthy,
		},
		{
			name: "running without a watermark is unknown, never stalled",
			in: taskHealthInput{Status: StatusRunning, Now: now,
				Replies: []taskHealthReply{unknownReply}, SessionLive: true},
			want: taskHealthHealthy,
		},
		{
			name: "running with nothing alive underneath is orphaned",
			in:   taskHealthInput{Status: StatusRunning, Now: now},
			want: taskHealthOrphaned,
		},
		{
			name: "running with open nodes is not orphaned",
			in:   taskHealthInput{Status: StatusRunning, Now: now, OpenNodes: true},
			want: taskHealthHealthy,
		},
		{
			name: "running with a live session but no reply row is healthy",
			in:   taskHealthInput{Status: StatusRunning, Now: now, SessionLive: true},
			want: taskHealthHealthy,
		},
		{
			name: "waiting_user with a replying session is waiting_busy",
			in: taskHealthInput{Status: StatusWaitingUser, Now: now,
				Replies: []taskHealthReply{freshReply}, SessionLive: true},
			want: taskHealthWaitingBusy,
		},
		{
			name: "waiting_user and idle is healthy",
			in:   taskHealthInput{Status: StatusWaitingUser, Now: now},
			want: taskHealthHealthy,
		},
		{
			name: "paused is healthy",
			in:   taskHealthInput{Status: StatusPaused, Now: now, Replies: []taskHealthReply{staleReply}},
			want: taskHealthHealthy,
		},
		{
			name: "terminal is healthy",
			in:   taskHealthInput{Status: StatusSuccess, Now: now},
			want: taskHealthHealthy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTaskHealth(tc.in); got != tc.want {
				t.Fatalf("classifyTaskHealth = %q, want %q", got, tc.want)
			}
		})
	}
}

type taskHealthFixture struct {
	svc    *Service
	store  *TaskStore
	rootID string
	taskID string
}

func newTaskHealthFixture(t *testing.T, taskID, status, mainSession string) *taskHealthFixture {
	t.Helper()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	if _, err := store.CreateTask(context.Background(),
		Task{ID: taskID, RootID: root.ID, Name: "巡检", Status: status, MainSessionKey: mainSession},
		TaskEvent{ID: taskID + "-created", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return &taskHealthFixture{svc: svc, store: store, rootID: root.ID, taskID: taskID}
}

func (f *taskHealthFixture) insertRunningReply(t *testing.T, id, sessionKey string, at time.Time) {
	t.Helper()
	if err := NewActivityStore(f.store.db).Insert(context.Background(), &TaskActivity{
		ID: id, TaskID: f.taskID, RootID: f.rootID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, SessionKey: sessionKey, CreatedAt: at,
	}); err != nil {
		t.Fatalf("insert reply: %v", err)
	}
}

func (f *taskHealthFixture) insertOpenNode(t *testing.T, runID, nodeRunID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.store.db.ExecContext(ctx, `
INSERT INTO workflow_runs (id, task_id, graph_json, created_at, updated_at)
VALUES (?, ?, '{}', '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`, runID, f.taskID); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := f.store.db.ExecContext(ctx, `
INSERT INTO workflow_node_runs (id, run_id, node_id, status, created_at, updated_at)
VALUES (?, ?, 'agent1', ?, '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`, nodeRunID, runID, NodeRunRunning); err != nil {
		t.Fatalf("insert node run: %v", err)
	}
}

func (f *taskHealthFixture) events(t *testing.T) []RouterEvent {
	t.Helper()
	events, err := NewRouterStore(f.store.db).ListEventsByTask(context.Background(), f.taskID)
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	var out []RouterEvent
	for _, ev := range events {
		if ev.Kind == RouterEventTurnStalled || ev.Kind == RouterEventTaskOrphaned {
			out = append(out, ev)
		}
	}
	return out
}

func TestTaskHealthSweepEmitsStalledTurnOncePerBucket(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	f := newTaskHealthFixture(t, "task-stall", StatusRunning, "sess-stall")
	f.insertRunningReply(t, "act-stall", "sess-stall", now.Add(-30*time.Minute))
	// 水位停在 12 分钟前：回合在飞，但一个字节都没再吐。
	f.svc.NoteSessionTurnOutput(ctx, f.rootID, "sess-stall", now.Add(-12*time.Minute))

	if _, err := f.svc.taskHealthSweep(ctx, f.rootID, now); err != nil {
		t.Fatalf("taskHealthSweep: %v", err)
	}
	events := f.events(t)
	if len(events) != 1 || events[0].Kind != RouterEventTurnStalled {
		t.Fatalf("events = %+v, want one turn_stalled", events)
	}
	payload := events[0].PayloadJSON
	for _, want := range []string{`"session_key":"sess-stall"`, `"activity_id":"act-stall"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %s missing %s", payload, want)
		}
	}

	// 同一现场、下一个拍（1 分钟后）：5 分钟桶内不重复报。
	if _, err := f.svc.taskHealthSweep(ctx, f.rootID, now.Add(time.Minute)); err != nil {
		t.Fatalf("taskHealthSweep again: %v", err)
	}
	if got := len(f.events(t)); got != 1 {
		t.Fatalf("events after re-sweep = %d, want 1 (bucket dedupe)", got)
	}

	// 过了桶边界、现场依旧：再报一条。
	if _, err := f.svc.taskHealthSweep(ctx, f.rootID, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("taskHealthSweep next bucket: %v", err)
	}
	if got := len(f.events(t)); got != 2 {
		t.Fatalf("events after next bucket = %d, want 2", got)
	}
}

func TestTaskHealthSweepEmitsOrphanedTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	f := newTaskHealthFixture(t, "task-orphan", StatusRunning, "sess-gone")

	if _, err := f.svc.taskHealthSweep(ctx, f.rootID, now); err != nil {
		t.Fatalf("taskHealthSweep: %v", err)
	}
	events := f.events(t)
	if len(events) != 1 || events[0].Kind != RouterEventTaskOrphaned {
		t.Fatalf("events = %+v, want one task_orphaned", events)
	}
	if !strings.Contains(events[0].PayloadJSON, `"session_key":"sess-gone"`) {
		t.Fatalf("payload %s missing session key", events[0].PayloadJSON)
	}
}

func TestTaskHealthSweepLeavesHealthyTasksAlone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// 在跑且有新鲜产出。
	fresh := newTaskHealthFixture(t, "task-fresh", StatusRunning, "sess-fresh")
	fresh.insertRunningReply(t, "act-fresh", "sess-fresh", now.Add(-5*time.Minute))
	fresh.svc.NoteSessionTurnOutput(ctx, fresh.rootID, "sess-fresh", now.Add(-time.Minute))

	// 在跑但底下有 open 节点：引擎随时会推进，不算孤儿。
	open := newTaskHealthFixture(t, "task-open", StatusRunning, "sess-open")
	open.insertOpenNode(t, "run-open", "nr-open")

	// waiting_user 且空闲：什么都不用做。
	idle := newTaskHealthFixture(t, "task-idle", StatusWaitingUser, "sess-idle")

	for _, f := range []*taskHealthFixture{fresh, open, idle} {
		if _, err := f.svc.taskHealthSweep(ctx, f.rootID, now); err != nil {
			t.Fatalf("taskHealthSweep %s: %v", f.taskID, err)
		}
		if events := f.events(t); len(events) != 0 {
			t.Fatalf("%s produced %+v, want no health events", f.taskID, events)
		}
	}
}
