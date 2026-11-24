package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 任务进终态后自动收尾：只排一次。重复投影 / 重复终态事件不能连跑两轮 diff 回合，
// 更不能把已经拆掉的 worktree 再拆一次。
func TestAutoFinishWorktreeQueuesOnlyOnce(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	exec := &fakeFinishExecutor{
		reply:   "done\n" + finishTurnDoneMarker,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.SetDirectExecutor(exec)
	releaseOnce := sync.OnceFunc(func() { close(exec.release) })
	defer releaseOnce()

	for i := 0; i < 3; i++ {
		if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
			t.Fatalf("set status: %v", err)
		}
	}
	// 第一轮的收尾回合停在 executor 里，此时 worktree 仍可用；后面的终态投影
	// 若没有事件去重就会再次起回合。这样事件计数才是幂等判据，而不是靠副作用
	// 恰好已经拆完树来「看起来只跑一次」。
	<-exec.started
	for i := 0; i < 3; i++ {
		svc.maybeAutoFinishWorktree(ctx, root.ID, taskID)
	}
	releaseOnce()
	waitForCondition(t, func() bool {
		exec.mu.Lock()
		prompt := exec.prompt
		exec.mu.Unlock()
		if !strings.Contains(prompt, "worktree 收尾") {
			return false
		}
		store, err := svc.taskStore(root.ID)
		if err != nil {
			return false
		}
		events, err := store.ListEvents(ctx, taskID)
		if err != nil {
			return false
		}
		for _, event := range events {
			if event.Type == "finish_auto_queued" {
				return true
			}
		}
		return false
	})
	store, _ := svc.taskStore(root.ID)
	events, _ := store.ListEvents(ctx, taskID)
	count := 0
	for _, event := range events {
		if event.Type == "finish_auto_queued" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finish_auto_queued events = %d, want 1", count)
	}
	// 收尾跑完后重新钉一棵可用树：Finishing 守卫此时已经放开，只剩事件去重
	// 能拦住第二轮。只靠「树已被拆掉」让测试通过会把去重回归漏掉。
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		_, err := os.Stat(worktreePath)
		if os.IsNotExist(err) && !svc.Finishing(root.ID, taskID) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, statErr := os.Stat(worktreePath)
	t.Logf("post finish statErr=%v finishing=%v", statErr, svc.Finishing(root.ID, taskID))
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	pinWorktree(t, store, mustGetTask(t, svc, root.ID, taskID), root.ID, worktreePath)
	svc.maybeAutoFinishWorktree(ctx, root.ID, taskID)
	svc.maybeAutoFinishWorktree(ctx, root.ID, taskID)
	events, _ = store.ListEvents(ctx, taskID)
	count = 0
	for _, event := range events {
		if event.Type == "finish_auto_queued" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finish_auto_queued events after re-pin = %d, want 1", count)
	}
	exec.mu.Lock()
	prompt := exec.prompt
	exec.mu.Unlock()
	if !strings.Contains(prompt, "worktree 收尾") {
		t.Fatalf("auto finish did not run the finish turn, prompt=%q", prompt)
	}
}

// 树干净、分支已并入主干时不需要模型回合：直接机械清场，隐式包含 repoint。
func TestAutoFinishWorktreeTearsDownCleanMergedTreeWithoutFinishTurn(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	exec := &fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker}
	svc.SetDirectExecutor(exec)

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("clean merged worktree was not torn down automatically, statErr=%v", err)
	}
	exec.mu.Lock()
	prompt := exec.prompt
	exec.mu.Unlock()
	if prompt != "" {
		t.Fatalf("mechanical teardown must not start a finish turn, prompt=%q", prompt)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "finish_auto_queued" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("finish_auto_queued events = %d, want 1", count)
	}
}

// 树干净但分支还没并入主干时，合并与潜在冲突仍需 agent 判断，不能走机械清场。
func TestAutoFinishWorktreeStartsTurnForCleanUnmergedTree(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	if err := os.WriteFile(filepath.Join(worktreePath, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("seed clean worktree commit: %v", err)
	}
	gitForTest(t, worktreePath, "add", "new.txt")
	gitForTest(t, worktreePath, "commit", "-qm", "worktree only")
	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	exec := &fakeFinishExecutor{
		reply:   "done\n" + finishTurnDoneMarker,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.SetDirectExecutor(exec)
	releaseOnce := sync.OnceFunc(func() { close(exec.release) })
	defer releaseOnce()

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	select {
	case <-exec.started:
	case <-time.After(5 * time.Second):
		t.Fatal("clean unmerged tree did not start the finish turn")
	}
	releaseOnce()
	exec.mu.Lock()
	prompt := exec.prompt
	exec.mu.Unlock()
	if !strings.Contains(prompt, "worktree 收尾") {
		t.Fatalf("clean unmerged tree must run the finish turn, prompt=%q", prompt)
	}
}

// 暂时性失败不能让任务永久失去自动收尾：失败事件解掉占坑，下一次终态可以重试。
func TestAutoFinishRetriesAfterFailure(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	svc.SetDirectExecutor(&fakeFinishExecutor{err: errors.New("temporary finish failure")})

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		events, err := store.ListEvents(ctx, taskID)
		if err == nil {
			for _, event := range events {
				if event.Type == "finish_auto_failed" && !svc.Finishing(root.ID, taskID) {
					goto failedObserved
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("finish_auto_failed was not recorded")
failedObserved:
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("failed finish must not silently remove the worktree: %v", err)
	}

	retry := &fakeFinishExecutor{
		reply:   "done\n" + finishTurnDoneMarker,
		started: make(chan struct{}),
	}
	svc.SetDirectExecutor(retry)
	svc.maybeAutoFinishWorktree(ctx, root.ID, taskID)
	select {
	case <-retry.started:
	case <-time.After(5 * time.Second):
		t.Fatal("auto finish did not retry after a recorded failure")
	}
	events, err := store.ListEvents(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for _, event := range events {
		if event.Type == "finish_auto_queued" {
			queued++
		}
	}
	if queued != 2 {
		t.Fatalf("finish_auto_queued events = %d, want 2 after retry", queued)
	}
}

func TestAutoFinishDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	svc.SetSessionRunningProbe(func(string) bool { return false })
	exec := &fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker}
	svc.SetDirectExecutor(exec)
	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatal(err)
	}
	store, _ := svc.taskStore(root.ID)
	events, _ := store.ListEvents(ctx, taskID)
	for _, event := range events {
		if event.Type == "finish_auto_queued" {
			t.Fatal("auto finish must stay off until explicitly enabled")
		}
	}
}
