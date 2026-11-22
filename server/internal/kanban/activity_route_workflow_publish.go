package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"strings"
)

// ActivityTypeWorkflowPublish 是「把这张图固化进目录」的待批准卡。载荷里带完整
// 请求（含整张图），所以批准时看到的就是将要写入的东西；卡片的 id 由决策 id
// 派生，重放不会插出第二张。
const ActivityTypeWorkflowPublish = "workflow_publish"

// workflowPublishCard 是卡片的 payload 形状。
type workflowPublishCard struct {
	Request    WorkflowPublishRequest `json:"request"`
	DecisionID string                 `json:"decision_id,omitempty"`
}

// applyRouteSaveWorkflow 落一次「固化 workflow」的决策。
//
// 两档判据见 workflowPublishNeedsApproval：项目级新条目直接写目录；全局条目与
// 「改已有」只落一张待批准卡，等人点批准 —— Router 永远不能自己把人该批的事批了。
func (s *ActivityService) applyRouteSaveWorkflow(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	req, err := s.workflowPublishRequest(ctx, action)
	if err != nil {
		return err
	}
	if s.workflows == nil {
		return errors.New("workflow control-plane store unavailable")
	}
	draft, err := s.workflows.GetDraft(ctx, req.WorkflowID)
	switch {
	case err == nil:
		req.BaseDraftRevision = draft.Revision
	case errors.Is(err, ErrDraftNotFound):
	default:
		return err
	}
	needsApproval, err := workflowPublishNeedsApproval(ctx, s.workflows, req)
	if err != nil {
		return err
	}
	if !needsApproval {
		version, err := commitWorkflowPublish(ctx, s.workflows, req, task.RootID, "router")
		if err != nil {
			return err
		}
		body := fmt.Sprintf("已把工作流「%s」（`%s`）固化为%s版本 `%s`。", req.Name, req.WorkflowID, scopeLabel(req.Scope), version.Version)
		return s.recordNodeAdminNote(ctx, decision, task, "workflow-publish-note:"+decision.ID, body)
	}
	return s.recordWorkflowPublishRequest(ctx, decision, task, req)
}

// workflowPublishRequest 把动作翻译成请求。派生模式下从目标条目已发布的最高版本
// 复制图 —— 记录 `derived_from`，不改来源条目的版本链。
func (s *ActivityService) workflowPublishRequest(ctx context.Context, action RouterAction) (WorkflowPublishRequest, error) {
	req := WorkflowPublishRequest{
		WorkflowID:  strings.TrimSpace(action.WorkflowID),
		Name:        strings.TrimSpace(action.WorkflowName),
		Scope:       strings.TrimSpace(action.Scope),
		DerivedFrom: strings.TrimSpace(action.DerivedFrom),
		Reason:      strings.TrimSpace(action.Reason),
	}
	if action.Graph != nil {
		// 补 agent 只改副本：决策里存的图还要原样重放。
		req.Graph = cloneWorkflowDefinition(*action.Graph)
		fillDefaultNodeAgent(req.Graph.Nodes)
	}
	req, err := req.Normalize()
	if err != nil {
		return req, err
	}
	if req.Graph.Nodes != nil {
		return req, nil
	}
	if req.DerivedFrom == "" {
		return req, errors.New("save_workflow requires graph or base_workflow_id")
	}
	if s.workflows == nil {
		return req, errors.New("workflow control-plane store unavailable")
	}
	latest, ok := s.latestVersion(ctx, req.DerivedFrom)
	if !ok {
		return req, fmt.Errorf("workflow %s has no published version to derive from", req.DerivedFrom)
	}
	base, err := s.workflows.GetVersion(ctx, latest)
	if err != nil {
		return req, err
	}
	graph := base.Graph
	graph.RootID = ""
	sanitized, err := sanitizeRouterGraph(&graph)
	if err != nil {
		return req, err
	}
	// 派生沿用来源的图，agent 同样要落成具体 harness。
	req.Graph = *sanitized
	return req, nil
}

// recordWorkflowPublishRequest 写待批准卡。状态停在 waiting_user：它是一条要人
// 拍板的行，不是已完成的通知。
func (s *ActivityService) recordWorkflowPublishRequest(ctx context.Context, decision *RouterDecision, task Task, req WorkflowPublishRequest) error {
	payload, err := json.Marshal(workflowPublishCard{Request: req, DecisionID: decision.ID})
	if err != nil {
		return err
	}
	act := &TaskActivity{
		ID:          "workflow-publish:" + decision.ID,
		TaskID:      task.ID,
		RootID:      task.RootID,
		Type:        ActivityTypeWorkflowPublish,
		Author:      "router",
		Role:        "system",
		Status:      ActivityStatusWaitingUser,
		Body:        workflowPublishCardBody(req),
		PayloadJSON: string(payload),
	}
	_, err = s.activities.InsertOrIgnore(ctx, act)
	return err
}

func workflowPublishCardBody(req WorkflowPublishRequest) string {
	scope := "当前项目"
	if req.Scope == WorkflowScopeGlobal {
		scope = "全局（所有项目）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Router 想把一版工作流固化进目录：**%s**（`%s`，%s，%d 个节点）。\n\n", req.Name, req.WorkflowID, scope, len(req.Graph.Nodes))
	if req.DerivedFrom != "" {
		fmt.Fprintf(&b, "来源：从 `%s` 派生。\n", req.DerivedFrom)
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, "理由：%s\n", req.Reason)
	}
	b.WriteString("\n批准后立即生效（发布为一个新版本）；驳回则什么都不写。")
	return b.String()
}

func scopeLabel(scope string) string {
	if scope == WorkflowScopeGlobal {
		return "全局"
	}
	return "项目级"
}
