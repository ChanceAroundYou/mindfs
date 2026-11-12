package kanban

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// Task 1 (Phase 1): Workflow metadata.
//
// The plan describes a SQL `workflows` table migration. This repository keeps
// workflows in the control-plane file store (`workflows.json`), so the schema
// change is a new `meta` map on that file. These tests pin the observable
// contract the rest of Phase 1 consumes: WorkflowMeta persistence plus
// GetMeta/ListAvailable/UpdateMeta.

// TestMigration005WorkflowMetadata is the Phase 1 Task 1 schema test: the four
// metadata fields must survive a store reload (the file-store equivalent of the
// planned ALTER TABLE columns).
func TestMigration005WorkflowMetadata(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newWorkflowStore(dir)
	want := &WorkflowMeta{
		ID:                "workflow-debug",
		Name:              "调试工作流",
		Description:       "定位和修复代码 bug",
		UseCases:          []string{"代码报错需要定位原因", "测试失败需要分析问题"},
		Tags:              []string{"debug", "troubleshoot", "fix"},
		AutoSelectEnabled: true,
	}
	if err := s.UpdateMeta(ctx, "workflow-debug", want); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	// A fresh store on the same dir must read the persisted metadata back.
	got, err := newWorkflowStore(dir).GetMeta(ctx, "workflow-debug")
	if err != nil {
		t.Fatalf("GetMeta after reload: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("meta round trip = %+v, want %+v", got, want)
	}
}

func TestWorkflowStore_GetMeta(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	meta := &WorkflowMeta{
		ID:                "wf-1",
		Name:              "Debug",
		Description:       "Fix bugs",
		UseCases:          []string{"error", "crash"},
		Tags:              []string{"debug"},
		AutoSelectEnabled: true,
	}
	if err := store.UpdateMeta(ctx, "wf-1", meta); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	got, err := store.GetMeta(ctx, "wf-1")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.Description != meta.Description {
		t.Fatalf("description = %q, want %q", got.Description, meta.Description)
	}
	if !reflect.DeepEqual(got.UseCases, meta.UseCases) {
		t.Fatalf("use cases = %v, want %v", got.UseCases, meta.UseCases)
	}
}

func TestWorkflowStore_GetMetaDefaultsForExistingWorkflow(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if _, err := s.CreateDraft(ctx, "wf-default", "", "agent", wfDef(wfNode("a", NodeKindAgent))); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	got, err := s.GetMeta(ctx, "wf-default")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ID != "wf-default" || got.Name != "wf-default" {
		t.Fatalf("default meta = %+v, want id/name = wf-default", got)
	}
	if !got.AutoSelectEnabled {
		t.Fatal("existing workflow must default to auto-select enabled")
	}
	if got.UseCases == nil || got.Tags == nil || len(got.UseCases) != 0 || len(got.Tags) != 0 {
		t.Fatalf("default collections must be non-nil and empty, got %+v", got)
	}
}

func TestWorkflowStore_GetMetaUnknown(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	if _, err := s.GetMeta(context.Background(), "missing"); !errors.Is(err, ErrWorkflowMetaNotFound) {
		t.Fatalf("GetMeta(missing) error = %v, want ErrWorkflowMetaNotFound", err)
	}
}

func TestWorkflowStore_UpdateMetaNormalizes(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	// ID comes from the argument, not the payload; nil collections become empty
	// so the persisted JSON shape is stable.
	if err := s.UpdateMeta(ctx, "wf-1", &WorkflowMeta{ID: "ignored", Name: "n"}); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	got, err := s.GetMeta(ctx, "wf-1")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.ID != "wf-1" {
		t.Fatalf("id = %q, want argument id", got.ID)
	}
	if got.UseCases == nil || got.Tags == nil {
		t.Fatalf("nil collections not normalized: %+v", got)
	}
}

func TestWorkflowStore_UpdateMetaValidation(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if err := s.UpdateMeta(ctx, "  ", &WorkflowMeta{Name: "x"}); err == nil {
		t.Fatal("empty id must be rejected")
	}
	if err := s.UpdateMeta(ctx, "wf", nil); err == nil {
		t.Fatal("nil meta must be rejected")
	}
}

func TestWorkflowStore_ListAvailableFiltersAutoSelect(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"wf-b", "wf-a", "wf-off"} {
		if _, err := s.CreateDraft(ctx, id, "", "agent", wfDef(wfNode("a", NodeKindAgent))); err != nil {
			t.Fatalf("CreateDraft(%s): %v", id, err)
		}
	}
	if err := s.UpdateMeta(ctx, "wf-a", &WorkflowMeta{Name: "A", Description: "alpha", AutoSelectEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMeta(ctx, "wf-off", &WorkflowMeta{Name: "Off", AutoSelectEnabled: false}); err != nil {
		t.Fatal(err)
	}
	// Metadata for a workflow that does not exist must not surface as available.
	if err := s.UpdateMeta(ctx, "wf-phantom", &WorkflowMeta{Name: "Phantom", AutoSelectEnabled: true}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListAvailable(ctx)
	if err != nil {
		t.Fatalf("ListAvailable: %v", err)
	}
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.ID)
	}
	want := []string{"wf-a", "wf-b"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ListAvailable ids = %v, want %v", ids, want)
	}
	// wf-b has no metadata row: it defaults from its id and stays available.
	if list[1].Name != "wf-b" || !list[1].AutoSelectEnabled {
		t.Fatalf("wf-b default meta = %+v", list[1])
	}
	if list[0].Description != "alpha" {
		t.Fatalf("wf-a description = %q", list[0].Description)
	}
}

func TestWorkflowStore_ListAvailableIncludesPublishedOnlyWorkflows(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if _, err := s.CreateDraft(ctx, "wf-pub", "", "agent", wfDef(wfNode("a", NodeKindAgent))); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDraft(ctx, "wf-pub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDraft(ctx, "wf-pub", d.Revision, "user"); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListAvailable(ctx)
	if err != nil {
		t.Fatalf("ListAvailable: %v", err)
	}
	if len(list) != 1 || list[0].ID != "wf-pub" {
		t.Fatalf("ListAvailable = %+v, want published workflow", list)
	}
}
