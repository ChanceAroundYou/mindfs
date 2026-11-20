package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
	// routerBriefTriggerBodyLimit 单独给「触发这一轮的留言」：它是 Router 唯一的
	// 决策输入，500 字节的摘录上限会把需求砍成半句话（实测 574 字节的需求被截在
	// 第 189 个字符，Router 转头去问用户第 5 条要什么）。仍设上限，避免一条超大
	// 粘贴把 brief 预算撑破；整体 24KB 的硬上限由 capSituationBrief 兜底。
	routerBriefTriggerBodyLimit = 8000
	routerBriefMaxWorkflows     = 20
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
	// 运行期事件回合的三段证据（Plan 2026-11-18，R1 T8）。用户留言回合保持 nil。
	Event    *RouterBriefEvent    `json:"event,omitempty"`
	Evidence *RouterBriefEvidence `json:"evidence,omitempty"`
	Budget   *RouterBriefBudget   `json:"budget,omitempty"`
	// Supervision 是主动监管的水位快照（事件生产时写入 payload，事件回合
	// 原样挂回 brief）。nil = 这个回合没有监管能力，工具也不会暴露。
	Supervision *SupervisionObservation `json:"supervision,omitempty"`
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

// turnBrief preserves the pre-dispatch shadow snapshot. Route decisions must observe
// the current execution and the original message when the task lane reaches that turn.
func (rs *RouterService) turnBrief(ctx context.Context, taskID string, trigger RouterTrigger, config RouterConfig) (*SituationBrief, error) {
	if trigger.Mode != RouterModeRoute && trigger.Brief != nil {
		return trigger.Brief, nil
	}
	store, err := rs.service.taskStore(rs.rootID)
	if err != nil {
		return nil, fmt.Errorf("load task store: %w", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task: %w", err)
	}
	brief, err := buildSituationBrief(ctx, store, rs.service.Workflows, config, task, trigger.Kind, trigger.UserActivityID)
	if err != nil {
		return nil, fmt.Errorf("build brief: %w", err)
	}
	// 运行期事件回合补证据三段；事件语义与用户留言不同，必须单独挂载后再按
	// 事件顺序重新裁剪（buildSituationBrief 只认用户回合的裁剪顺序）。
	if trigger.Event != nil {
		if err := attachRouterEventBrief(ctx, store, brief, *trigger.Event); err != nil {
			return nil, fmt.Errorf("attach event brief: %w", err)
		}
		// 主动监管：决策时重算水位，而不是复用事件生产时写下的快照 —— 预算
		// （每节点已用重试、最近一小时动作数）必须是最新的，否则同一轮里连发的
		// 两个事件会各自看到「还有余额」。
		if config.Supervision.Normalized().Mode != RouterSupervisionOff && strings.TrimSpace(trigger.Event.RunID) != "" {
			if run, runErr := store.WorkflowRunByID(ctx, trigger.Event.RunID); runErr == nil && run != nil {
				if task, taskErr := store.GetTask(ctx, trigger.Event.TaskID); taskErr == nil {
					brief.Supervision = rs.service.supervisionObservation(ctx, store, task, run, time.Now().UTC())
				}
			}
		}
		brief = capSituationBrief(brief)
	}
	return brief, nil
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
			"latest_run.node_summary.unknown 表示进程重启时那一轮结果不明：节点已不在跑，可以用 router_read_session 读它的会话说清结果，再决定是否新开一轮",
			"router_read_session 默认给最后一段文本，用 offset 往前翻；只能读本任务的会话",
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
					Body:           truncateRouterText(item.Body, routerBriefTriggerBodyLimit),
					WorkflowChoice: item.WorkflowChoice,
					At:             item.CreatedAt.UTC().Format(rfc3339),
				}
			}
		}
	}
	if run, err := store.WorkflowRunForTask(ctx, task.ID); err == nil && run != nil {
		// NodeRun 行是活动执行的另一半证据：投影只在节点终态写，重启把 running
		// 标成 unknown 时投影还是空的（#86）。一次取出，闸门与摘要共用。
		nodeRuns, _ := store.ListWorkflowNodeRuns(ctx, run.ID)
		brief.LatestRun = briefRun(ctx, store, run, task, nodeRuns)
		// 闸门按「Run 投影后的状态」判，而不是可能滞后的任务行：任务行写着
		// waiting_user、节点其实还在跑时，Router 会被告知「没有活动执行」，于是
		// 把一条本该转交的留言判成新开一轮。
		//
		// 但「活动」必须还有可变化的节点：整轮 success 的 Run 投影到 waiting_user
		// 只是为了让用户来审核，它已经没有节点会自己再动一下。若仍报成活动执行，
		// 校验层会永久拒绝 run_workflow / message_agent，Router 只能反复劝用户
		// 「先结算 Run」——而界面上根本没有这个入口（#88 现场）。
		// 暂停是操作员状态，不是「这一轮跑完了」：暂停任务照旧挡新执行。
		status := briefTaskStatus(run, task)
		if isActiveTaskStatus(status) && (status == StatusPaused || workflowRunHasOpenNodes(run, nodeRuns)) {
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

// briefTaskStatus 是 brief 里「任务现在到底在干什么」的唯一来源。
//
// 任务行的 status 由另一条投影写入，可能落后于 Run 的节点事实（实测：任务行
// waiting_user、节点却在 running）。brief 是 Router 的唯一世界快照，陈旧状态会
// 直接变成错误决策，所以这里以 Run 投影为准；只有操作员拥有的状态（暂停）与
// 任务已经终态这两种「投影看不见 / 不该覆盖」的情况才采信任务行。
func briefTaskStatus(run *WorkflowRun, task Task) string {
	if task.Status == StatusPaused || isTerminalStatus(task.Status) {
		return task.Status
	}
	if run == nil {
		return task.Status
	}
	derived := taskStatusFromRun(run)
	// 投影是节点事实，但两份证据都可能落后：任务行会停在上一轮的 waiting_user，
	// 而 NodeStates 在节点刚 claim 的那一瞬间也可能还没写进去（NodeRun 行已经是
	// running）。两边都说「有活动」时以投影为准；一边说在跑、另一边说空闲时采信
	// 更活跃的那个——宁可多报一次活动，也不要把一条本该转交的留言判成新开一轮。
	//
	// 但终态的 Run 只有在任务行也是终态时才算任务结论。任务行不是终态，说明这
	// 条 Run 只是上一轮的证据（被取代、或用户点了「回到待审核」）：采信任务行，
	// 否则 apply 闸门会拿旧结论把新留言判成「任务已结束」。
	if isTerminalStatus(derived) {
		return task.Status
	}
	if isActiveTaskStatus(derived) {
		return derived
	}
	if isActiveTaskStatus(task.Status) {
		return task.Status
	}
	if derived != "" {
		return derived
	}
	return task.Status
}

// briefRun 汇总 Run 的节点状态。nodeRuns 是调用方一次取出的行（可传 nil，只
// 看投影）。
//
// 投影只在节点终态时写，所以「空投影 + unknown 行」（#86）必须由行补上：否则
// 摘要永远是 0 个节点，Router 看不到这一轮到底做过什么。
func briefRun(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task, nodeRuns []NodeRun) *RouterBriefRun {
	if run == nil {
		return nil
	}
	summary := map[string]int{"total": 0, "success": 0, "running": 0, "pending": 0, "failed": 0, "waiting_user": 0, "cancelled": 0, "unknown": 0}
	waiting := make([]string, 0)
	for _, status := range run.NodeStates {
		summary["total"]++
		if bucket := briefRunBucket(status); bucket != "" {
			summary[bucket]++
		}
	}
	// NodeStates 不保留节点名，也不保留还没写进投影的节点；用行补齐。
	if nodeRuns == nil && store != nil {
		nodeRuns, _ = store.ListWorkflowNodeRuns(ctx, run.ID)
	}
	for _, nodeRun := range nodeRuns {
		if nodeRun.Status == NodeRunWaitingUser {
			waiting = append(waiting, nodeRun.NodeID)
		}
	}
	for nodeID, status := range latestNodeRunStatus(nodeRuns) {
		if _, ok := run.NodeStates[nodeID]; ok {
			continue
		}
		if bucket := briefRunBucket(status); bucket != "" {
			summary["total"]++
			summary[bucket]++
		}
	}
	return &RouterBriefRun{
		ID:               run.ID,
		Status:           briefTaskStatus(run, task),
		Version:          run.Version,
		NodeSummary:      summary,
		WaitingUserNodes: waiting,
	}
}

// briefRunBucket 把节点状态映射到 node_summary 的桶；未知状态返回空串，
// 宁可少报一个桶也不要把状态名拼进 map 里造出没人认识的新桶。
func briefRunBucket(status string) string {
	switch status {
	case NodeRunSuccess:
		return "success"
	case NodeRunRunning:
		return "running"
	case NodeRunPending, NodeRunQueued:
		return "pending"
	case NodeRunFail:
		return "failed"
	case NodeRunWaitingUser:
		return "waiting_user"
	case NodeRunCancelled:
		return "cancelled"
	case NodeRunUnknown:
		return "unknown"
	default:
		return ""
	}
}

// latestNodeRunStatus 取每个节点最新一次尝试的状态。行只是投影的补充证据：
// 投影里有的节点以投影为准，这里只用来补投影还没写到的节点。
func latestNodeRunStatus(nodeRuns []NodeRun) map[string]string {
	out := make(map[string]string, len(nodeRuns))
	attempts := make(map[string]int, len(nodeRuns))
	for _, nodeRun := range nodeRuns {
		if nodeRun.LoopPath != "" {
			continue
		}
		if prev, ok := attempts[nodeRun.NodeID]; ok && prev > nodeRun.Attempt {
			continue
		}
		attempts[nodeRun.NodeID] = nodeRun.Attempt
		out[nodeRun.NodeID] = nodeRun.Status
	}
	return out
}

func briefActiveExecution(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) *RouterBriefExecution {
	execution := &RouterBriefExecution{
		Kind:      "workflow",
		RunID:     run.ID,
		Status:    briefTaskStatus(run, task),
		StartedAt: run.CreatedAt.UTC().Format(rfc3339),
	}
	if store == nil {
		return execution
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return execution
	}
	kinds := workflowNodeKinds(run)
	for _, nodeRun := range runs {
		if kinds[nodeRun.NodeID] != NodeKindAgent || nodeRun.Status != NodeRunRunning {
			continue
		}
		// A running Agent without a session still counts towards store uniqueness.
		// Even another, bound Agent cannot accept implicit delivery in that case.
		if strings.TrimSpace(nodeRun.SessionKey) == "" {
			execution.CurrentNodes = nil
			return execution
		}
		execution.CurrentNodes = append(execution.CurrentNodes, RouterBriefNode{
			NodeID:         nodeRun.NodeID,
			Name:           nodeRun.NodeID,
			Status:         nodeRun.Status,
			SessionPresent: true,
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
