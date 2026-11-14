package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Situation Brief 是 Router 一轮能看到的世界摘要。它是**快照**：shadow 模式必须
// 在旧路径分发之前取，否则比较的是「旧路径已经改变了世界之后」的状态。
//
// 信任边界：只含本账户任务数据与 workflow 元数据；不含文件内容、shell 输出、
// agent 转录、环境变量或其它任务/账户的数据。workflow 的 name/description 按
// 不可信数据处理（提示词要求把它们当数据，不当指令）。

const (
	routerBriefVersion         = 1
	routerBriefMaxBytes        = 24 * 1024
	routerBriefMaxActivities   = 10
	routerBriefActivityExcerpt = 500
	routerBriefMaxWorkflows    = 20
)

type SituationBrief struct {
	Version          int                   `json:"version"`
	Task             RouterBriefTask       `json:"task"`
	Trigger          RouterBriefTrigger    `json:"trigger"`
	RecentActivities []RouterBriefActivity `json:"recent_activities,omitempty"`
	ActiveExecution  *RouterBriefExecution `json:"active_execution,omitempty"`
	LatestRun        *RouterBriefRun       `json:"latest_run,omitempty"`
	Workflows        []RouterBriefWorkflow `json:"workflows,omitempty"`
	Limits           RouterBriefLimits     `json:"limits"`
	PolicyHints      []string              `json:"policy_hints,omitempty"`
}

type RouterBriefTask struct {
	ID                 string `json:"id"`
	RootID             string `json:"root_id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	CreateWorktree     bool   `json:"create_worktree"`
	WorktreePresent    bool   `json:"worktree_present"`
	MainSessionPresent bool   `json:"main_session_present"`
}

type RouterBriefTrigger struct {
	Kind        RouterTriggerKind       `json:"kind"`
	Ref         string                  `json:"ref,omitempty"`
	UserMessage *RouterBriefUserMessage `json:"user_message,omitempty"`
}

type RouterBriefUserMessage struct {
	ID             string `json:"id"`
	Body           string `json:"body"`
	WorkflowChoice string `json:"workflow_choice"`
	At             string `json:"at"`
}

type RouterBriefActivity struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Role        string `json:"role"`
	Purpose     string `json:"purpose,omitempty"`
	Status      string `json:"status,omitempty"`
	BodyExcerpt string `json:"body_excerpt,omitempty"`
	At          string `json:"at"`
}

type RouterBriefExecution struct {
	Kind         string            `json:"kind"` // workflow|direct|none
	RunID        string            `json:"run_id,omitempty"`
	ActivityID   string            `json:"activity_id,omitempty"`
	Status       string            `json:"status,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	CurrentNodes []RouterBriefNode `json:"current_nodes,omitempty"`
}

type RouterBriefNode struct {
	NodeID         string `json:"node_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	SessionPresent bool   `json:"session_present"`
}

type RouterBriefRun struct {
	ID               string         `json:"id"`
	Status           string         `json:"status,omitempty"`
	Version          string         `json:"version,omitempty"`
	NodeSummary      map[string]int `json:"node_summary,omitempty"`
	WaitingUserNodes []string       `json:"waiting_user_nodes,omitempty"`
	ErrorExcerpt     string         `json:"error_excerpt,omitempty"`
}

type RouterBriefWorkflow struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	UseCases      []string `json:"use_cases,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	LatestVersion string   `json:"latest_version,omitempty"`
}

type RouterBriefLimits struct {
	MaxSay     int `json:"max_say"`
	MaxActions int `json:"max_actions"`
	DeadlineMS int `json:"deadline_ms"`
}

func (b *SituationBrief) Hash() string {
	if b == nil {
		return ""
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// buildSituationBrief 只读任务库与 workflow 目录，不写任何状态。
func buildSituationBrief(
	ctx context.Context,
	store *TaskStore,
	workflows *WorkflowStore,
	config RouterConfig,
	task Task,
	kind RouterTriggerKind,
	userActivityID string,
) (*SituationBrief, error) {
	if store == nil {
		return nil, errors.New("task store unavailable")
	}
	brief := &SituationBrief{
		Version: routerBriefVersion,
		Task: RouterBriefTask{
			ID:                 task.ID,
			RootID:             task.RootID,
			Name:               strings.TrimSpace(task.Name),
			Status:             task.Status,
			CreateWorktree:     task.CreateWorktree,
			WorktreePresent:    strings.TrimSpace(task.WorktreePath) != "",
			MainSessionPresent: strings.TrimSpace(task.MainSessionKey) != "",
		},
		Trigger: RouterBriefTrigger{Kind: kind, Ref: userActivityID},
		Limits: RouterBriefLimits{
			MaxSay:     config.MaxSay,
			MaxActions: config.MaxActions,
			DeadlineMS: int(config.TurnTimeout.Milliseconds()),
		},
		PolicyHints: []string{
			"有活动执行时不要新开 Run 或 direct",
			"不确定时用 router_ask_user，不要猜",
			"不能批准 HumanGate，不能 merge worktree",
		},
	}
	activities := NewActivityStore(store.db)
	recent, err := activities.List(ctx, task.ID)
	if err == nil {
		brief.RecentActivities = briefActivities(recent, userActivityID)
		if userActivityID != "" {
			if item, err := activities.Get(ctx, userActivityID); err == nil && item != nil {
				brief.Trigger.UserMessage = &RouterBriefUserMessage{
					ID:             item.ID,
					Body:           truncateRouterText(item.Body, routerBriefActivityExcerpt),
					WorkflowChoice: item.WorkflowChoice,
					At:             item.CreatedAt.UTC().Format(rfc3339),
				}
			}
		}
	}
	if run, err := store.WorkflowRunForTask(ctx, task.ID); err == nil && run != nil {
		brief.LatestRun = briefRun(ctx, store, run, task)
		if isActiveTaskStatus(task.Status) {
			brief.ActiveExecution = briefActiveExecution(ctx, store, run, task)
		}
	}
	brief.Workflows = briefWorkflows(ctx, workflows)
	return capSituationBrief(brief), nil
}

func briefActivities(items []*TaskActivity, triggerUserActivityID string) []RouterBriefActivity {
	if len(items) == 0 {
		return nil
	}
	start := len(items) - routerBriefMaxActivities
	if start < 0 {
		start = 0
	}
	out := make([]RouterBriefActivity, 0, len(items)-start)
	for _, item := range items[start:] {
		if item == nil {
			continue
		}
		out = append(out, RouterBriefActivity{
			ID:          item.ID,
			Type:        item.Type,
			Role:        item.Role,
			Purpose:     item.Purpose,
			Status:      item.Status,
			BodyExcerpt: truncateRouterText(item.Body, routerBriefActivityExcerpt),
			At:          item.CreatedAt.UTC().Format(rfc3339),
		})
	}
	return out
}

func briefRun(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) *RouterBriefRun {
	if run == nil {
		return nil
	}
	summary := map[string]int{"total": 0, "success": 0, "running": 0, "pending": 0, "failed": 0, "waiting_user": 0, "cancelled": 0}
	waiting := make([]string, 0)
	for _, status := range run.NodeStates {
		summary["total"]++
		switch status {
		case NodeRunSuccess:
			summary["success"]++
		case NodeRunRunning:
			summary["running"]++
		case NodeRunPending, NodeRunQueued:
			summary["pending"]++
		case NodeRunFail:
			summary["failed"]++
		case NodeRunWaitingUser:
			summary["waiting_user"]++
		case NodeRunCancelled:
			summary["cancelled"]++
		}
	}
	// NodeStates 不保留节点名；用 NodeRun 行补 waiting_user 的节点 ID。
	if runs, err := store.ListWorkflowNodeRuns(ctx, run.ID); err == nil {
		for _, nodeRun := range runs {
			if nodeRun.Status == NodeRunWaitingUser {
				waiting = append(waiting, nodeRun.NodeID)
			}
		}
	}
	return &RouterBriefRun{
		ID:               run.ID,
		Status:           task.Status,
		Version:          run.Version,
		NodeSummary:      summary,
		WaitingUserNodes: waiting,
	}
}

func briefActiveExecution(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) *RouterBriefExecution {
	execution := &RouterBriefExecution{
		Kind:      "workflow",
		RunID:     run.ID,
		Status:    task.Status,
		StartedAt: run.CreatedAt.UTC().Format(rfc3339),
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return execution
	}
	for _, nodeRun := range runs {
		if nodeRun.Status != NodeRunRunning && nodeRun.Status != NodeRunWaitingUser {
			continue
		}
		execution.CurrentNodes = append(execution.CurrentNodes, RouterBriefNode{
			NodeID:         nodeRun.NodeID,
			Name:           nodeRun.NodeID,
			Status:         nodeRun.Status,
			SessionPresent: strings.TrimSpace(nodeRun.SessionKey) != "",
		})
	}
	return execution
}

func briefWorkflows(ctx context.Context, workflows *WorkflowStore) []RouterBriefWorkflow {
	if workflows == nil {
		return nil
	}
	available, err := workflows.ListAvailable(ctx)
	if err != nil {
		return nil
	}
	versions, _ := workflows.ListVersions(ctx)
	latest := make(map[string]string, len(versions))
	for _, version := range versions {
		if current := latest[version.WorkflowID]; current == "" || workflowVersionSeq(version.Version) > workflowVersionSeq(current) {
			latest[version.WorkflowID] = version.Version
		}
	}
	out := make([]RouterBriefWorkflow, 0, len(available))
	for _, meta := range available {
		if meta == nil {
			continue
		}
		out = append(out, RouterBriefWorkflow{
			ID:            meta.ID,
			Name:          truncateRouterText(meta.Name, 200),
			Description:   truncateRouterText(meta.Description, 500),
			UseCases:      truncateRouterList(meta.UseCases, 8, 100),
			Tags:          truncateRouterList(meta.Tags, 8, 100),
			LatestVersion: latest[meta.ID],
		})
		if len(out) >= routerBriefMaxWorkflows {
			break
		}
	}
	return out
}

// capSituationBrief 按「先砍 workflows，再砍 recent_activities，再砍 excerpt」
// 的顺序把 brief 压到上限内。24KB 是硬上限，宁可少信息也不撑爆上下文。
func capSituationBrief(brief *SituationBrief) *SituationBrief {
	if brief == nil {
		return nil
	}
	if briefSize(brief) <= routerBriefMaxBytes {
		return brief
	}
	brief.Workflows = nil
	if briefSize(brief) <= routerBriefMaxBytes {
		return brief
	}
	for len(brief.RecentActivities) > 0 && briefSize(brief) > routerBriefMaxBytes {
		brief.RecentActivities = brief.RecentActivities[1:]
	}
	for _, activity := range brief.RecentActivities {
		if briefSize(brief) <= routerBriefMaxBytes {
			break
		}
		activity.BodyExcerpt = truncateRouterText(activity.BodyExcerpt, 200)
	}
	if brief.Trigger.UserMessage != nil && briefSize(brief) > routerBriefMaxBytes {
		brief.Trigger.UserMessage.Body = truncateRouterText(brief.Trigger.UserMessage.Body, 2000)
	}
	return brief
}

func briefSize(brief *SituationBrief) int {
	raw, err := json.Marshal(brief)
	if err != nil {
		return 0
	}
	return len(raw)
}

func isActiveTaskStatus(status string) bool {
	switch status {
	case StatusRunning, StatusWaitingUser, StatusPaused:
		return true
	default:
		return false
	}
}

func truncateRouterText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "…"
}

func truncateRouterList(items []string, maxItems, maxLen int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = truncateRouterText(item, maxLen)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"
