package kanban

import (
	"context"
	"errors"
	"testing"
)

// 只挂在 Meta 里、没有草稿也没有版本的条目不是工作流：
// 导出成记录会把一条空壳带到对方机器上。
func TestExportForSyncSkipsOrphanMeta(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if err := store.UpdateMeta(ctx, "orphan", &WorkflowMeta{ID: "orphan", Name: "孤儿"}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	if _, err := store.CreateDraft(ctx, "real", "", "agent", wfDef(wfNode("a", NodeKindAgent))); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	state, err := store.ExportForSync()
	if err != nil {
		t.Fatalf("ExportForSync: %v", err)
	}
	for _, meta := range state.Metas {
		if meta.ID == "orphan" {
			t.Fatalf("孤儿元数据被导出了: %+v", state.Metas)
		}
	}
	if len(state.Metas) != 1 || state.Metas[0].ID != "real" {
		t.Fatalf("导出集合不对: %+v", state.Metas)
	}
}

func TestRemoveForSyncIsIdempotentAndCleansEverything(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	draft, err := store.CreateDraft(ctx, "wf1", "", "agent", wfDef(wfNode("a", NodeKindAgent)))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := store.PublishDraft(ctx, "wf1", draft.Revision, "agent"); err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}
	if err := store.UpdateMeta(ctx, "wf1", &WorkflowMeta{ID: "wf1", Name: "一号"}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	if err := store.RemoveForSync("wf1"); err != nil {
		t.Fatalf("RemoveForSync: %v", err)
	}
	state, err := store.ExportForSync()
	if err != nil {
		t.Fatalf("ExportForSync: %v", err)
	}
	if len(state.Metas)+len(state.Drafts)+len(state.Versions) != 0 {
		t.Fatalf("删完还有残留: %+v", state)
	}
	// 第二次删同一个 id 必须成功（墓碑可能被重放）。
	if err := store.RemoveForSync("wf1"); err != nil {
		t.Fatalf("重复 RemoveForSync: %v", err)
	}
}

func TestApplyVersionForSyncRejectsForeignVersionID(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	err := store.ApplyVersionForSync(WorkflowVersion{WorkflowID: "wf1", Version: "wf2:v1", Revision: "r1"})
	if err == nil {
		t.Fatalf("版本 id 归属不对也被接受了")
	}
	if errors.Is(err, ErrWorkflowVersionImmutable) {
		t.Fatalf("err = %v，这是格式错误不是不可变冲突", err)
	}
}
