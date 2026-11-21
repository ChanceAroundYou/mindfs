package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// W3 T2：把 Router 起草的 workflow 固化进目录，两档判据 —— 项目级新条目直接发布，
// 全局条目与「改已有」必须人批准。这些用例钉住「谁有权写目录」这条边界。

func newPublishFixture(t *testing.T) (*TaskStore, *Service, Task, *WorkflowStore) {
	t.Helper()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-publish", "run-publish", twoAgentGraph())
	workflows := NewWorkflowStoreAt(t.TempDir())
	svc.Workflows = workflows
	task, err := store.GetTask(context.Background(), run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	_ = root
	return store, svc, task, workflows
}

func publishActivityService(t *testing.T, svc *Service, rootID string) *ActivityService {
	t.Helper()
	activity, err := svc.ActivityServiceForRoot(rootID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	return activity
}

func publishGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "locate", Name: "定位", Kind: NodeKindAgent, Params: map[string]any{"prompt": "找到问题"}},
			{ID: "fix", Name: "修复", Kind: NodeKindAgent, Params: map[string]any{"prompt": "{previous_output}"}},
		},
		Edges: []WorkflowEdge{{From: "locate", To: "fix"}},
	}
}

func publishAction(scope string) RouterAction {
	graph := publishGraph()
	return RouterAction{
		Kind:         RouterActionSaveWorkflow,
		WorkflowID:   "wf-review",
		WorkflowName: "代码复查",
		Scope:        scope,
		Graph:        &graph,
		Reason:       "这个任务反复用到同一套步骤",
	}
}

func publishCards(t *testing.T, store *TaskStore, taskID string) []*TaskActivity {
	t.Helper()
	acts, err := NewActivityStore(store.db).List(context.Background(), taskID)
	if err != nil {
		t.Fatalf("List activities: %v", err)
	}
	var out []*TaskActivity
	for _, act := range acts {
		if act.Type == ActivityTypeWorkflowPublish {
			out = append(out, act)
		}
	}
	return out
}

func versionsOf(t *testing.T, store *WorkflowStore, workflowID string) []WorkflowVersion {
	t.Helper()
	all, err := store.ListVersions(context.Background())
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	var out []WorkflowVersion
	for _, v := range all {
		if v.WorkflowID == workflowID {
			out = append(out, v)
		}
	}
	return out
}

// 项目级新条目：免批，直接发布，并且作用域落在任务所在的项目上。
func TestApplySaveWorkflowProjectScopePublishesImmediately(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	decision := &RouterDecision{ID: "dec-publish-1", TaskID: task.ID, RootID: task.RootID}
	activity := publishActivityService(t, svc, task.RootID)
	if err := activity.applyRouteSaveWorkflow(context.Background(), decision, task, publishAction(WorkflowScopeProject)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}

	versions := versionsOf(t, workflows, "wf-review")
	if len(versions) != 1 {
		t.Fatalf("want 1 published version, got %d", len(versions))
	}
	if versions[0].Graph.RootID != task.RootID {
		t.Fatalf("project-scope version root = %q, want task root %q", versions[0].Graph.RootID, task.RootID)
	}
	if got := len(versions[0].Graph.Nodes); got != 2 {
		t.Fatalf("published graph nodes = %d, want 2", got)
	}
	// 发布出去的版本必须能直接跑：Router 只写了 prompt，agent 要在固化时补上。
	for _, n := range versions[0].Graph.Nodes {
		if got, _ := n.Params["agent"].(string); strings.TrimSpace(got) != defaultRouterAgent {
			t.Fatalf("published node %q agent = %q, want %q", n.ID, got, defaultRouterAgent)
		}
	}
	if cards := publishCards(t, store, task.ID); len(cards) != 0 {
		t.Fatalf("project-scope new workflow must not ask for approval, got %d cards", len(cards))
	}
	acts, err := NewActivityStore(store.db).List(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("List activities: %v", err)
	}
	found := false
	for _, act := range acts {
		if strings.Contains(act.Body, versions[0].Version) {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeline has no note carrying version %s", versions[0].Version)
	}
}

// 提升到全局：一张待批准卡，目录里什么都不写。
func TestApplySaveWorkflowGlobalScopeRequiresApproval(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	decision := &RouterDecision{ID: "dec-publish-2", TaskID: task.ID, RootID: task.RootID}
	activity := publishActivityService(t, svc, task.RootID)
	if err := activity.applyRouteSaveWorkflow(context.Background(), decision, task, publishAction(WorkflowScopeGlobal)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}

	if versions := versionsOf(t, workflows, "wf-review"); len(versions) != 0 {
		t.Fatalf("global scope must not publish before approval, got %d versions", len(versions))
	}
	if exists, err := workflows.WorkflowExists(context.Background(), "wf-review"); err != nil || exists {
		t.Fatalf("catalog must stay untouched, exists=%v err=%v", exists, err)
	}
	cards := publishCards(t, store, task.ID)
	if len(cards) != 1 {
		t.Fatalf("want exactly 1 approval card, got %d", len(cards))
	}
	if cards[0].Status != ActivityStatusWaitingUser {
		t.Fatalf("card status = %q, want %q", cards[0].Status, ActivityStatusWaitingUser)
	}
	var card workflowPublishCard
	if err := json.Unmarshal([]byte(cards[0].PayloadJSON), &card); err != nil {
		t.Fatalf("card payload: %v", err)
	}
	if card.Request.Scope != WorkflowScopeGlobal || len(card.Request.Graph.Nodes) != 2 {
		t.Fatalf("card payload lost the request: %+v", card.Request)
	}
	// 卡片里的图就是批准后要发布的图：批准之前就得是能跑的。
	for _, n := range card.Request.Graph.Nodes {
		if got, _ := n.Params["agent"].(string); strings.TrimSpace(got) == "" {
			t.Fatalf("card node %q has no agent: %+v", n.ID, n.Params)
		}
	}
	if card.Request.BaseDraftRevision != "" {
		t.Fatalf("a brand-new workflow has no base revision, got %q", card.Request.BaseDraftRevision)
	}
}

// 「改已有」一律要批准 —— 哪怕是项目级。
func TestApplySaveWorkflowExistingIDRequiresApproval(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	graph := publishGraph()
	if _, err := commitWorkflowPublish(ctx, workflows, WorkflowPublishRequest{
		WorkflowID: "wf-review", Name: "代码复查", Scope: WorkflowScopeProject, Graph: graph,
	}, task.RootID, "user"); err != nil {
		t.Fatalf("seed publish: %v", err)
	}
	decision := &RouterDecision{ID: "dec-publish-3", TaskID: task.ID, RootID: task.RootID}
	activity := publishActivityService(t, svc, task.RootID)
	if err := activity.applyRouteSaveWorkflow(ctx, decision, task, publishAction(WorkflowScopeProject)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}
	if versions := versionsOf(t, workflows, "wf-review"); len(versions) != 1 {
		t.Fatalf("modifying an existing workflow must not publish before approval, got %d versions", len(versions))
	}
	cards := publishCards(t, store, task.ID)
	if len(cards) != 1 || cards[0].Status != ActivityStatusWaitingUser {
		t.Fatalf("want 1 waiting card for a modify request, got %+v", cards)
	}
	var card workflowPublishCard
	if err := json.Unmarshal([]byte(cards[0].PayloadJSON), &card); err != nil {
		t.Fatalf("card payload: %v", err)
	}
	if card.Request.BaseDraftRevision == "" {
		t.Fatal("modify request must pin the draft revision it was written against")
	}
}

// 升级前落下的待批准卡，图里没有 agent。批准时不补，旧卡批出来的版本仍然
// 一执行就 "agent required" —— 卡片可能已经在队列里躺了几天。
func TestResolveWorkflowPublishFillsAgentOnLegacyCard(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	req, err := WorkflowPublishRequest{
		WorkflowID: "wf-legacy-card",
		Name:       "旧卡",
		Scope:      WorkflowScopeGlobal,
		Graph:      publishGraph(),
		Reason:     "升级前落下的待批准卡",
	}.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	activity := publishActivityService(t, svc, task.RootID)
	decision := &RouterDecision{ID: "dec-legacy-card", TaskID: task.ID, RootID: task.RootID}
	if err := activity.recordWorkflowPublishRequest(ctx, decision, task, req); err != nil {
		t.Fatalf("recordWorkflowPublishRequest: %v", err)
	}
	cards := publishCards(t, store, task.ID)
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	if _, err := svc.ResolveWorkflowPublish(ctx, task.RootID, task.ID, cards[0].ID, "approve", ""); err != nil {
		t.Fatalf("ResolveWorkflowPublish(approve): %v", err)
	}
	versions := versionsOf(t, workflows, "wf-legacy-card")
	if len(versions) != 1 {
		t.Fatalf("want 1 published version, got %d", len(versions))
	}
	for _, n := range versions[0].Graph.Nodes {
		if got, _ := n.Params["agent"].(string); strings.TrimSpace(got) == "" {
			t.Fatalf("legacy card published node %q without agent: %+v", n.ID, n.Params)
		}
	}
}

// 批准：真正写控制面，全局条目的 root 为空，卡片转 completed。
func TestResolveWorkflowPublishApprovePublishesGlobal(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	decision := &RouterDecision{ID: "dec-publish-4", TaskID: task.ID, RootID: task.RootID}
	if err := publishActivityService(t, svc, task.RootID).applyRouteSaveWorkflow(ctx, decision, task, publishAction(WorkflowScopeGlobal)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}
	cards := publishCards(t, store, task.ID)
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}

	act, err := svc.ResolveWorkflowPublish(ctx, task.RootID, task.ID, cards[0].ID, "approve", "")
	if err != nil {
		t.Fatalf("ResolveWorkflowPublish(approve): %v", err)
	}
	if act.Status != ActivityStatusCompleted {
		t.Fatalf("card status after approval = %q, want completed", act.Status)
	}
	versions := versionsOf(t, workflows, "wf-review")
	if len(versions) != 1 {
		t.Fatalf("want 1 published version after approval, got %d", len(versions))
	}
	if versions[0].Graph.RootID != "" {
		t.Fatalf("global version root = %q, want empty (global)", versions[0].Graph.RootID)
	}
	// 重复批准是幂等拒绝，不是再发布一次。
	if _, err := svc.ResolveWorkflowPublish(ctx, task.RootID, task.ID, cards[0].ID, "approve", ""); !errors.Is(err, ErrWorkflowPublishDecided) {
		t.Fatalf("second approval err = %v, want ErrWorkflowPublishDecided", err)
	}
	if versions := versionsOf(t, workflows, "wf-review"); len(versions) != 1 {
		t.Fatalf("re-approval published again: %d versions", len(versions))
	}
}

// 驳回：卡片转 cancelled，目录一个字节都不写。
func TestResolveWorkflowPublishRejectWritesNothing(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	decision := &RouterDecision{ID: "dec-publish-5", TaskID: task.ID, RootID: task.RootID}
	if err := publishActivityService(t, svc, task.RootID).applyRouteSaveWorkflow(ctx, decision, task, publishAction(WorkflowScopeGlobal)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}
	cards := publishCards(t, store, task.ID)
	act, err := svc.ResolveWorkflowPublish(ctx, task.RootID, task.ID, cards[0].ID, "reject", "先不要固化")
	if err != nil {
		t.Fatalf("ResolveWorkflowPublish(reject): %v", err)
	}
	if act.Status != ActivityStatusCancelled {
		t.Fatalf("card status after reject = %q, want cancelled", act.Status)
	}
	if !strings.Contains(act.Body, "先不要固化") {
		t.Fatalf("reject note lost: %q", act.Body)
	}
	if exists, err := workflows.WorkflowExists(ctx, "wf-review"); err != nil || exists {
		t.Fatalf("reject must not touch the catalog, exists=%v err=%v", exists, err)
	}
}

// 草稿在等待批准期间被改过：批准被拒（CAS），卡片转 failed，不覆盖别人的改动。
func TestResolveWorkflowPublishStaleDraftFails(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	graph := publishGraph()
	if _, err := commitWorkflowPublish(ctx, workflows, WorkflowPublishRequest{
		WorkflowID: "wf-review", Name: "代码复查", Scope: WorkflowScopeProject, Graph: graph,
	}, task.RootID, "user"); err != nil {
		t.Fatalf("seed publish: %v", err)
	}
	decision := &RouterDecision{ID: "dec-publish-6", TaskID: task.ID, RootID: task.RootID}
	if err := publishActivityService(t, svc, task.RootID).applyRouteSaveWorkflow(ctx, decision, task, publishAction(WorkflowScopeProject)); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}
	// 等待期间有人（例如另一个会话）改了草稿。
	draft, err := workflows.GetDraft(ctx, "wf-review")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	edited := publishGraph()
	edited.Nodes = append(edited.Nodes, WorkflowNode{ID: "check", Name: "复核", Kind: NodeKindAgent, Params: map[string]any{"prompt": "复核"}})
	edited.Edges = append(edited.Edges, WorkflowEdge{From: "fix", To: "check"})
	if _, err := workflows.UpdateDraft(ctx, "wf-review", draft.Revision, "user", edited); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	cards := publishCards(t, store, task.ID)
	act, err := svc.ResolveWorkflowPublish(ctx, task.RootID, task.ID, cards[0].ID, "approve", "")
	if !errors.Is(err, ErrWorkflowPublishStale) {
		t.Fatalf("stale approval err = %v, want ErrWorkflowPublishStale", err)
	}
	if act.Status != ActivityStatusFailed {
		t.Fatalf("stale card status = %q, want failed", act.Status)
	}
	if versions := versionsOf(t, workflows, "wf-review"); len(versions) != 1 {
		t.Fatalf("stale approval must not publish, got %d versions", len(versions))
	}
}

// 提出请求时目录里还没有这个 id，等待批准期间别人把它建了出来：批准看的
// 「新条目」已经不存在了，必须按 stale 失败，绝不静默覆盖别人刚建的草稿。
func TestCommitWorkflowPublishStaleWhenEntryAppearedAfterRequest(t *testing.T) {
	_, _, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	other := publishGraph()
	other.Nodes = append(other.Nodes, WorkflowNode{ID: "check", Name: "复核", Kind: NodeKindAgent, Params: map[string]any{"prompt": "复核"}})
	other.Edges = append(other.Edges, WorkflowEdge{From: "fix", To: "check"})
	if _, err := workflows.CreateDraft(ctx, "wf-review", task.RootID, "someone", other); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	_, err := commitWorkflowPublish(ctx, workflows, WorkflowPublishRequest{
		WorkflowID: "wf-review", Name: "代码复查", Scope: WorkflowScopeProject, Graph: publishGraph(),
	}, task.RootID, "user")
	if !errors.Is(err, ErrWorkflowPublishStale) {
		t.Fatalf("commit err = %v, want ErrWorkflowPublishStale", err)
	}
	draft, err := workflows.GetDraft(ctx, "wf-review")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if len(draft.Graph.Nodes) != len(other.Nodes) {
		t.Fatalf("别人的草稿被覆盖了：%+v", draft.Graph.Nodes)
	}
	if versions := versionsOf(t, workflows, "wf-review"); len(versions) != 0 {
		t.Fatalf("stale 提交不得发布版本，got %d", len(versions))
	}
}

// 派生：只给 base_workflow_id 时从已发布最高版本复制图，并记下来源。
func TestSaveWorkflowDerivesGraphFromBase(t *testing.T) {
	store, svc, task, workflows := newPublishFixture(t)
	ctx := context.Background()
	base := publishGraph()
	base.RootID = ""
	if _, err := commitWorkflowPublish(ctx, workflows, WorkflowPublishRequest{
		WorkflowID: "wf-base", Name: "基础流程", Scope: WorkflowScopeGlobal, Graph: base,
	}, task.RootID, "user"); err != nil {
		t.Fatalf("seed base: %v", err)
	}
	decision := &RouterDecision{ID: "dec-publish-7", TaskID: task.ID, RootID: task.RootID}
	action := RouterAction{
		Kind:         RouterActionSaveWorkflow,
		WorkflowID:   "wf-derived",
		WorkflowName: "派生流程",
		Scope:        WorkflowScopeProject,
		DerivedFrom:  "wf-base",
		Reason:       "在这个项目里改两点",
	}
	if err := publishActivityService(t, svc, task.RootID).applyRouteSaveWorkflow(ctx, decision, task, action); err != nil {
		t.Fatalf("applyRouteSaveWorkflow: %v", err)
	}
	versions := versionsOf(t, workflows, "wf-derived")
	if len(versions) != 1 {
		t.Fatalf("want 1 derived version, got %d", len(versions))
	}
	if len(versions[0].Graph.Nodes) != len(base.Nodes) {
		t.Fatalf("derived graph nodes = %d, want %d copied from base", len(versions[0].Graph.Nodes), len(base.Nodes))
	}
	if versions[0].Graph.RootID != task.RootID {
		t.Fatalf("derived project-scope root = %q, want %q", versions[0].Graph.RootID, task.RootID)
	}
	meta, err := workflows.GetMeta(ctx, "wf-derived")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if len(meta.Tags) == 0 || meta.Tags[0] != "derived:wf-base" {
		t.Fatalf("derived tag = %v, want [derived:wf-base]", meta.Tags)
	}
	_ = store
}

// 工具层：graph 与 base_workflow_id 互斥，两个都不给也是错。
func TestSaveWorkflowToolRejectsAmbiguousSources(t *testing.T) {
	store, svc, task, _ := newPublishFixture(t)
	_ = svc
	env := nodeAdminToolEnv(t, store, task.ID)
	tool := toolByName(t, env, "router_save_workflow")
	ctx := context.Background()

	cases := []struct {
		name string
		args string
		want string
	}{
		{"neither graph nor base", `{"workflow_id":"wf-x","scope":"project"}`, "graph or base_workflow_id"},
		{"both graph and base", `{"workflow_id":"wf-x","scope":"project","base_workflow_id":"wf-y","graph":{"nodes":[{"id":"a","kind":"agent"}]}}`, "not both"},
		{"bad scope", `{"workflow_id":"wf-x","scope":"everyone","graph":{"nodes":[{"id":"a","kind":"agent"}]}}`, "scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Handler(ctx, json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// 校验层：图的形状问题在动作校验里就要拒，不能等到写目录时才炸。
func TestValidateRouterActionsSaveWorkflowGraph(t *testing.T) {
	dangling := publishGraph()
	dangling.Edges = []WorkflowEdge{{From: "locate", To: "missing"}}
	action := RouterAction{
		Seq: 0, Kind: RouterActionSaveWorkflow,
		WorkflowID: "wf-x", Scope: WorkflowScopeProject, Graph: &dangling,
	}
	if err := validateRouterActions(nil, []RouterAction{action}, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("dangling edge in save_workflow graph must be rejected")
	}
}
