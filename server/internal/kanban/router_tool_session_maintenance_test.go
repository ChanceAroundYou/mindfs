package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mindfs/server/internal/sessionmaintenance"
)

func TestRouterSessionMaintenanceToolRecordsWriteActions(t *testing.T) {
	collector := newRouterTurnCollector(RouterConfig{})
	env := &routerToolEnv{
		brief:             &SituationBrief{Task: RouterBriefTask{ID: "task-1", RootID: "root-1"}},
		collector:         collector,
		sessionMaintainer: &stubSessionMaintainer{},
	}
	tool := env.sessionMaintenanceTool()

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"diagnose"}`))
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if !strings.Contains(out, `"scanned":1`) || len(collector.snapshot()) != 0 {
		t.Fatalf("diagnose must be read-only, got %q and actions %#v", out, collector.snapshot())
	}

	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"clean_locks"}`)); err != nil {
		t.Fatalf("clean_locks: %v", err)
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"strip_images","session_key":"s1"}`)); err != nil {
		t.Fatalf("strip_images: %v", err)
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"purge_empty","session_key":"s1","confirm":true}`)); err != nil {
		t.Fatalf("purge_empty: %v", err)
	}
	actions := collector.snapshot()
	if len(actions) != 3 {
		t.Fatalf("unexpected actions: %#v", actions)
	}
	if actions[0].MaintenanceAction != "clean_locks" || actions[1].SessionKey != "s1" || !actions[2].Confirm {
		t.Fatalf("write actions did not carry maintenance fields: %#v", actions)
	}
}

func TestRouterSessionMaintenanceToolRequiresConfirmation(t *testing.T) {
	collector := newRouterTurnCollector(RouterConfig{})
	env := &routerToolEnv{
		brief:             &SituationBrief{Task: RouterBriefTask{ID: "task-1", RootID: "root-1"}},
		collector:         collector,
		sessionMaintainer: &stubSessionMaintainer{},
	}
	tool := env.sessionMaintenanceTool()
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"action":"purge_empty","session_key":"s1"}`))
	if err == nil || !strings.Contains(err.Error(), "confirm:true") {
		t.Fatalf("purge without confirmation must be rejected, got %v", err)
	}
	if len(collector.snapshot()) != 0 {
		t.Fatalf("rejected tool call must not record an action")
	}
}

func TestApplyRouteSessionMaintenanceRechecksConfirmation(t *testing.T) {
	stub := &stubSessionMaintainer{}
	svc := &ActivityService{sessionMaintainer: stub}
	err := svc.applyRouteSessionMaintenance(context.Background(), &RouterDecision{}, Task{RootID: "root-1"}, RouterAction{
		Kind: RouterActionSessionMaintenance, MaintenanceAction: "purge_empty", SessionKey: "s1",
	})
	if err == nil || !strings.Contains(err.Error(), "confirm:true") {
		t.Fatalf("apply must recheck confirmation, got %v", err)
	}
	if stub.purged != 0 {
		t.Fatalf("apply must not purge without confirmation")
	}
}

type stubSessionMaintainer struct {
	purged int
}

func (s *stubSessionMaintainer) SessionMaintenanceDiagnose(context.Context, string) (sessionmaintenance.Report, error) {
	return sessionmaintenance.Report{Scanned: 1}, nil
}

func (s *stubSessionMaintainer) SessionMaintenanceCleanLocks(context.Context) (sessionmaintenance.LockResult, error) {
	return sessionmaintenance.LockResult{}, nil
}

func (s *stubSessionMaintainer) SessionMaintenanceCleanImages(context.Context, string, string) ([]sessionmaintenance.CleanResult, error) {
	return nil, nil
}

func (s *stubSessionMaintainer) SessionMaintenancePurgeEmpty(context.Context, string, string, bool) ([]sessionmaintenance.CleanResult, error) {
	s.purged++
	return nil, nil
}
