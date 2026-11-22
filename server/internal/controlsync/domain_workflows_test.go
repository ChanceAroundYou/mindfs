package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"mindfs/server/internal/kanban"
)

func testWorkflowGraph() kanban.WorkflowDefinition {
	return kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{
		{ID: "a", Name: "a", Kind: kanban.NodeKindAgent, Params: map[string]any{"prompt": "hi"}},
	}}
}

func newWorkflowSyncStore(t *testing.T) *kanban.WorkflowStore {
	t.Helper()
	return kanban.NewWorkflowStoreAt(t.TempDir())
}

func seedWorkflow(t *testing.T, store *kanban.WorkflowStore, id, name string) {
	t.Helper()
	ctx := context.Background()
	draft, err := store.CreateDraft(ctx, id, "", "agent", testWorkflowGraph())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := store.UpdateDraft(ctx, id, draft.Revision, "agent", testWorkflowGraph()); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if _, err := store.PublishDraft(ctx, id, "r2", "agent"); err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}
	if err := store.UpdateMeta(ctx, id, &kanban.WorkflowMeta{ID: id, Name: name}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
}

func workflowSnapshot(t *testing.T, store *kanban.WorkflowStore) []Record {
	t.Helper()
	recs, err := NewWorkflowsDomain(store).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return recs
}

func TestWorkflowsDomainSnapshotCoversMetaDraftAndVersions(t *testing.T) {
	store := newWorkflowSyncStore(t)
	seedWorkflow(t, store, "wf1", "一号流程")
	keys := recordKeys(workflowSnapshot(t, store))
	for _, want := range []string{"wf:wf1:meta", "wf:wf1:draft", "wf:wf1:version:2"} {
		if _, ok := findRecord(workflowSnapshot(t, store), want); !ok {
			t.Fatalf("缺记录 %q：%v", want, keys)
		}
	}
}

func TestWorkflowsDomainApplyRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := newWorkflowSyncStore(t)
	seedWorkflow(t, source, "wf1", "一号流程")
	target := newWorkflowSyncStore(t)
	domain := NewWorkflowsDomain(target)
	for _, rec := range workflowSnapshot(t, source) {
		if err := domain.Apply(ctx, rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
	}

	meta, err := target.GetMeta(ctx, "wf1")
	if err != nil || meta == nil || meta.Name != "一号流程" {
		t.Fatalf("元数据没搬过去: meta=%+v err=%v", meta, err)
	}
	drafts, err := target.ListDrafts(ctx)
	if err != nil || len(drafts) != 1 || drafts[0].Revision != "r2" {
		t.Fatalf("草稿没搬过去: %+v err=%v", drafts, err)
	}
	if _, err := target.GetVersion(ctx, "wf1:v2"); err != nil {
		t.Fatalf("版本没搬过去: %v", err)
	}

	// seq 游标必须跟着记录走：否则目标机下一次编辑会退回 r1 / 撞上 v2。
	draft, err := target.UpdateDraft(ctx, "wf1", "r2", "agent", testWorkflowGraph())
	if err != nil {
		t.Fatalf("UpdateDraft on target: %v", err)
	}
	if draft.Revision != "r3" {
		t.Fatalf("应用后草稿 revision = %q，应为 r3（seq 游标没跟上）", draft.Revision)
	}
	version, err := target.PublishDraft(ctx, "wf1", "r3", "agent")
	if err != nil {
		t.Fatalf("PublishDraft on target: %v", err)
	}
	if version.Version != "wf1:v3" {
		t.Fatalf("新版本 = %q，应为 wf1:v3（否则与已有的 v2 撞号）", version.Version)
	}
}

func TestWorkflowsDomainVersionIsImmutable(t *testing.T) {
	ctx := context.Background()
	source := newWorkflowSyncStore(t)
	seedWorkflow(t, source, "wf1", "一号流程")
	target := newWorkflowSyncStore(t)
	domain := NewWorkflowsDomain(target)
	var versionRec Record
	for _, rec := range workflowSnapshot(t, source) {
		if err := domain.Apply(ctx, rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
		if rec.Key == "wf:wf1:version:2" {
			versionRec = rec
		}
	}
	if versionRec.Key == "" {
		t.Fatalf("没找到版本记录")
	}
	var version kanban.WorkflowVersion
	if err := json.Unmarshal(versionRec.Payload, &version); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	version.Revision = "r9"
	mutated, err := newRecord(DomainWorkflows, versionRec.Key, version)
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(ctx, mutated); !errors.Is(err, kanban.ErrWorkflowVersionImmutable) {
		t.Fatalf("改了内容的历史版本被接受: %v", err)
	}
	// 原样重放仍然幂等。
	if err := domain.Apply(ctx, versionRec); err != nil {
		t.Fatalf("重放同一版本应成功: %v", err)
	}
}

func TestWorkflowsDomainMetaTombstoneRemovesWorkflow(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowSyncStore(t)
	seedWorkflow(t, store, "wf1", "一号流程")
	domain := NewWorkflowsDomain(store)
	if err := domain.Apply(ctx, Record{Domain: DomainWorkflows, Key: "wf:wf1:meta", Deleted: true}); err != nil {
		t.Fatalf("Apply tombstone: %v", err)
	}
	if _, err := store.GetMeta(ctx, "wf1"); !errors.Is(err, kanban.ErrWorkflowMetaNotFound) {
		t.Fatalf("墓碑之后元数据还在: %v", err)
	}
	if drafts, _ := store.ListDrafts(ctx); len(drafts) != 0 {
		t.Fatalf("墓碑之后草稿还在: %+v", drafts)
	}
	if versions, _ := store.ListVersions(ctx); len(versions) != 0 {
		t.Fatalf("墓碑之后版本还在: %+v", versions)
	}
}

func TestWorkflowsDomainRejectsBadRecords(t *testing.T) {
	store := newWorkflowSyncStore(t)
	domain := NewWorkflowsDomain(store)
	must := func(key string, payload any) Record {
		t.Helper()
		rec, err := newRecord(DomainWorkflows, key, payload)
		if err != nil {
			t.Fatalf("newRecord: %v", err)
		}
		return rec
	}
	cases := []struct {
		name string
		rec  Record
		want error
	}{
		{"版本不能单独删除", Record{Domain: DomainWorkflows, Key: "wf:wf1:version:1", Deleted: true}, nil},
		{"草稿不能单独删除", Record{Domain: DomainWorkflows, Key: "wf:wf1:draft", Deleted: true}, nil},
		{"key 与载荷 id 不符", must("wf:wf1:meta", kanban.WorkflowMeta{ID: "wf2"}), errWorkflowKeyMismatch},
		{"版本号与 key 不符", must("wf:wf1:version:3", kanban.WorkflowVersion{
			Version: "wf1:v2", WorkflowID: "wf1", Revision: "r2",
		}), errWorkflowKeyMismatch},
		{"坏 key", Record{Domain: DomainWorkflows, Key: "wf1"}, nil},
		{"未知域", Record{Domain: DomainNodes, Key: "wf:wf1:meta"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.Apply(context.Background(), tc.rec)
			if err == nil {
				t.Fatalf("坏记录被接受了")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if metas, _ := store.ExportForSync(); len(metas.Metas)+len(metas.Drafts)+len(metas.Versions) != 0 {
		t.Fatalf("坏记录改了工作流库: %+v", metas)
	}
}

func TestWorkflowsDomainOlderDraftDoesNotRollBack(t *testing.T) {
	ctx := context.Background()
	source := newWorkflowSyncStore(t)
	seedWorkflow(t, source, "wf1", "一号流程")
	target := newWorkflowSyncStore(t)
	domain := NewWorkflowsDomain(target)
	var draftRec Record
	for _, rec := range workflowSnapshot(t, source) {
		if err := domain.Apply(ctx, rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
		if rec.Key == "wf:wf1:draft" {
			draftRec = rec
		}
	}
	var draft kanban.WorkflowDraft
	if err := json.Unmarshal(draftRec.Payload, &draft); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	draft.Revision = "r1"
	older, err := newRecord(DomainWorkflows, draftRec.Key, draft)
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(ctx, older); err != nil {
		t.Fatalf("Apply(older): %v", err)
	}
	drafts, err := target.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("ListDrafts: %v", err)
	}
	if len(drafts) != 1 || drafts[0].Revision != "r2" {
		t.Fatalf("旧草稿把现有内容回退了: %+v", drafts)
	}
}

// 从未发布过的工作流只有草稿：seq 游标只能从草稿推，推不上去就会退版号。
func TestWorkflowsDomainSeqFollowsDraftWithoutVersions(t *testing.T) {
	ctx := context.Background()
	source := newWorkflowSyncStore(t)
	draft, err := source.CreateDraft(ctx, "wf2", "", "agent", testWorkflowGraph())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	for i := 0; i < 2; i++ {
		if draft, err = source.UpdateDraft(ctx, "wf2", draft.Revision, "agent", testWorkflowGraph()); err != nil {
			t.Fatalf("UpdateDraft: %v", err)
		}
	}
	if draft.Revision != "r3" {
		t.Fatalf("前置条件不成立：revision = %q", draft.Revision)
	}
	target := newWorkflowSyncStore(t)
	domain := NewWorkflowsDomain(target)
	for _, rec := range workflowSnapshot(t, source) {
		if err := domain.Apply(ctx, rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
	}
	next, err := target.UpdateDraft(ctx, "wf2", "r3", "agent", testWorkflowGraph())
	if err != nil {
		t.Fatalf("UpdateDraft on target: %v", err)
	}
	if next.Revision != "r4" {
		t.Fatalf("应用后 revision = %q，应为 r4（草稿的 seq 没推上游标）", next.Revision)
	}
}
