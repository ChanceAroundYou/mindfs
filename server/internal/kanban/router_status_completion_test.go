package kanban

import (
	"context"
	"testing"
	"time"
)

// 行为：诊断快照要能回答「最近一小时有多少次收尾被证据门拦下」——它是自动补做
// 是否真在发生的唯一现成计量，不该靠翻决策表。
func TestRouterStatusCountsCompletionBlocks(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOff)
	now := time.Now().UTC()
	insert := func(id string, kind RouterTriggerKind, at time.Time) {
		t.Helper()
		must(t, NewRouterStore(f.store.db).InsertDecision(context.Background(), &RouterDecision{
			ID: id, TaskID: "task-1", RootID: f.root.ID, Scope: RouterScopeTask, OwnerID: "task-1",
			TriggerKind: kind, TurnID: "turn-" + id,
			Status: RouterDecisionApplied, CreatedAt: at,
		}), "InsertDecision(%s)", id)
	}
	insert("block-now", RouterTriggerCompletionBlocked, now.Add(-time.Minute))
	insert("block-old", RouterTriggerCompletionBlocked, now.Add(-2*time.Hour))
	insert("failed-now", RouterTriggerRunFailed, now.Add(-time.Minute))

	status := f.svc.RouterStatusForRoot(f.root.ID)
	if status.CompletionBlocksLastHour != 1 {
		t.Fatalf("completion blocks last hour = %d, want 1", status.CompletionBlocksLastHour)
	}
}
