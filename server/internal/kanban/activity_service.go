package kanban

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Task 5 (Phase 2): ActivityService 把「用户留言」翻译成「WorkflowRun + Agent 回复」。
//
// 与计划 sketch 的差异（都是本仓库的现实约束，不是取舍）：
//   - 计划里的 WorkflowStore.CreateRun/Execute 不存在：workflow 版本在控制面
//     WorkflowStore，运行记录却是数据面的 TaskStore 记录（workflow_runs 表），
//     由既有 workflow driver 推进。所以这里 Resolve 版本用 WorkflowStore，
//     CreateWorkflowRunAndSetEngine 用 TaskStore，执行交给注入的
//     ActivityWorkflowExecutor 端口；没有端口时 Agent 回复停在 pending，由
//     运行层通过 OnWorkflowRunFailed/OnWorkflowRunCompleted 回报终态。
//   - 计划里的 StreamHub 住在 api 包（kanban 不能反向 import），广播用
//     ActivityBroadcaster 接口注入，Task 7 再接 WebSocket。
//   - 计划里的 ActivityService 直接持有 sessionManager；本仓库的 workflow
//     Agent 节点由 WorkflowNodeRunner 端口执行，不需要也不应该在这里再持有一份。
//
// 顺序约束：先落 Agent 回复（pending）再建 Run。Run 建失败时回复转 failed 并
// 带上错误；反过来会出现「跑起来了但对话流里看不见」的隐藏执行。

// ErrEmptyActivityBody 表示用户提交了空留言。API 层按文案映射 400。
var ErrEmptyActivityBody = errors.New("body cannot be empty")

// ActivityBroadcaster 接收活动流生命周期事件。生产实现是 api 层的 StreamHub
// 适配器；nil 时静默（单测/老装配行为不变）。
type ActivityBroadcaster interface {
	ActivityCreated(activity *TaskActivity)
	ActivityUpdated(activity *TaskActivity, changes map[string]any)
}

// ActivityWorkflowExecutor 是 Router 交给工作流运行层的端口：只负责把 Run
// 唤醒/推进，终态由运行层在真正结算的那一步回写（settle）。
//
// 这里刻意不返回 summary：唤醒是异步的，唤醒后立刻返回一个总结会把
// 「已经开始」误报成「已经完成」——2026-11-14 实测过：占位回复在 0ms 就变成
// completed，而节点还在跑，之后也没人再回写。
type ActivityWorkflowExecutor interface {
	ExecuteActivityWorkflow(ctx context.Context, task Task, run *WorkflowRun) error
}

// ActivityDirectExecutor 是 Router 的直连执行端口（workflow_choice=disabled）：
// 把用户这句话作为普通回合发给任务的会话，返回 Agent 的回复正文。
type ActivityDirectExecutor interface {
	RunDirectActivity(ctx context.Context, task Task, body string) (string, error)
}

// ActivityBranchResolver 在创建 Run 前解析合并目标分支（D11：运行期绝不隐式
// 回落 main）。生产装配指向 Service.snapshotRunIntegrationBranch；nil 时留空，
// 只对不需要独立 worktree 的图安全。
type ActivityBranchResolver func(ctx context.Context, task Task, graph WorkflowDefinition) (string, error)

type ActivityService struct {
	tasks          *TaskStore
	activities     *ActivityStore
	workflows      *WorkflowStore
	recommender    *WorkflowRecommender
	broadcaster    ActivityBroadcaster
	executor       ActivityWorkflowExecutor
	directExecutor ActivityDirectExecutor
	branch         ActivityBranchResolver
}

// NewActivityService builds the service for one task node. workflows/recommender
// may be nil for workflow_choice=disabled; other choices then fail visibly.
func NewActivityService(tasks *TaskStore, workflows *WorkflowStore, recommender *WorkflowRecommender) *ActivityService {
	svc := &ActivityService{tasks: tasks, workflows: workflows, recommender: recommender}
	if tasks != nil {
		svc.activities = NewActivityStore(tasks.db)
	}
	return svc
}

// SetBroadcaster 装配广播适配器。装配期调用一次。
func (s *ActivityService) SetBroadcaster(b ActivityBroadcaster) {
	if s != nil {
		s.broadcaster = b
	}
}

// SetWorkflowExecutor 装配 WorkflowRun 执行端口。装配期调用一次。
func (s *ActivityService) SetWorkflowExecutor(e ActivityWorkflowExecutor) {
	if s != nil {
		s.executor = e
	}
}

// SetDirectExecutor 装配直连执行端口（workflow_choice=disabled）。
func (s *ActivityService) SetDirectExecutor(e ActivityDirectExecutor) {
	if s != nil {
		s.directExecutor = e
	}
}

// SetBranchResolver 装配 Run 创建时的合并目标解析器。装配期调用一次。
func (s *ActivityService) SetBranchResolver(r ActivityBranchResolver) {
	if s != nil {
		s.branch = r
	}
}

// CreateUserActivity 创建用户留言，并按 workflow_choice 产生 Agent 回复：
//   - empty：推荐/默认一个已发布 workflow，创建 WorkflowRun；
//   - specified：使用调用方指定的 workflow 版本，创建 WorkflowRun；
//   - disabled：不建 Run，只留一条 pending 的 Agent 回复给直连执行路径。
//
// 返回值是（用户活动, Agent 活动, error）。空留言返回 ErrEmptyActivityBody。
func (s *ActivityService) CreateUserActivity(
	ctx context.Context,
	taskID string,
	body string,
	workflowChoice string,
	workflowVersionID *string,
) (*TaskActivity, *TaskActivity, error) {
	if s == nil || s.tasks == nil || s.activities == nil {
		return nil, nil, errors.New("activity service not configured")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, nil, ErrEmptyActivityBody
	}
	choice, err := normalizeWorkflowChoice(workflowChoice)
	if err != nil {
		return nil, nil, err
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}

	userAct := &TaskActivity{
		ID:             newID("act"),
		TaskID:         task.ID,
		RootID:         task.RootID,
		Type:           ActivityTypeUserMessage,
		Role:           RoleUser,
		Body:           body,
		WorkflowChoice: choice,
	}
	if err := s.activities.Insert(ctx, userAct); err != nil {
		return nil, nil, err
	}
	s.broadcastCreated(userAct)

	var agentAct *TaskActivity
	if choice == CommentWorkflowChoiceDisabled {
		agentAct, err = s.startDirectAgent(ctx, task, body)
	} else {
		agentAct, err = s.startWorkflowRun(ctx, task, body, choice, workflowVersionID, userAct.ID)
	}
	return userAct, agentAct, err
}

// startDirectAgent 是「纯对话模式」的一轮：插入 pending 占位后，把用户这句
// 话作为普通回合交给直连执行端口。没有端口就显式失败，绝不留下永久 pending。
func (s *ActivityService) startDirectAgent(ctx context.Context, task Task, body string) (*TaskActivity, error) {
	agentAct := &TaskActivity{
		ID:             newID("act"),
		TaskID:         task.ID,
		RootID:         task.RootID,
		Type:           ActivityTypeAgentReply,
		Role:           RoleAgent,
		Body:           "",
		Status:         ActivityStatusPending,
		WorkflowChoice: CommentWorkflowChoiceDisabled,
	}
	if err := s.activities.Insert(ctx, agentAct); err != nil {
		return nil, err
	}
	s.broadcastCreated(agentAct)
	if s.directExecutor == nil {
		s.failActivity(ctx, agentAct, errors.New("direct executor not configured"))
		return agentAct, nil
	}
	go s.executeDirectActivity(context.WithoutCancel(ctx), agentAct, task, body)
	return agentAct, nil
}

// executeDirectActivity 跑一轮普通对话并回写占位回复。
// 正文只写 Agent 的真实输出：运行中/无输出/失败这些「壳」由前端按 locale 渲染。
func (s *ActivityService) executeDirectActivity(ctx context.Context, agentAct *TaskActivity, task Task, body string) {
	_ = s.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil)
	s.broadcastUpdatedByID(ctx, agentAct.ID, map[string]any{"status": ActivityStatusRunning})

	text, err := s.directExecutor.RunDirectActivity(ctx, task, body)
	if err != nil {
		failure := strings.TrimSpace(err.Error())
		_ = s.activities.UpdateBody(ctx, agentAct.ID, failure)
		_ = s.finishActivityByID(ctx, agentAct.ID, ActivityStatusFailed, failure)
		return
	}
	_ = s.activities.UpdateBody(ctx, agentAct.ID, strings.TrimSpace(text))
	_ = s.finishActivityByID(ctx, agentAct.ID, ActivityStatusCompleted, "")
}

// startWorkflowRun 创建 pending 的 Agent 回复并落一个 WorkflowRun。
//
// Run 创建失败不向上抛：用户留言已经落库，把失败写回 Agent 回复（status=failed
// + error summary）比让整条留言消失更符合对话模型。真正的输入错误（空留言、
// 非法 choice）在调用方已经提前拒绝。
func (s *ActivityService) startWorkflowRun(
	ctx context.Context,
	task Task,
	userInput string,
	choice string,
	versionID *string,
	userActivityID string,
) (*TaskActivity, error) {
	agentAct := &TaskActivity{
		ID:             newID("act"),
		TaskID:         task.ID,
		RootID:         task.RootID,
		Type:           ActivityTypeAgentReply,
		Role:           RoleAgent,
		Body:           "",
		Status:         ActivityStatusPending,
		WorkflowChoice: choice,
	}
	if err := s.activities.Insert(ctx, agentAct); err != nil {
		return nil, err
	}
	s.broadcastCreated(agentAct)

	version, err := s.resolveWorkflowVersion(ctx, userInput, choice, versionID)
	if err == nil {
		err = s.persistWorkflowRun(ctx, task, version, userActivityID, agentAct)
	}
	if err != nil {
		s.failActivity(ctx, agentAct, err)
		return agentAct, nil
	}
	return agentAct, nil
}

// persistWorkflowRun creates the data-plane run, links it to the agent activity,
// and (when an executor is wired) starts asynchronous execution.
func (s *ActivityService) persistWorkflowRun(ctx context.Context, task Task, version WorkflowVersion, userActivityID string, agentAct *TaskActivity) error {
	if s.tasks == nil {
		return errors.New("task store unavailable")
	}
	run := &WorkflowRun{
		ID:             newRunID(),
		TaskID:         task.ID,
		Version:        version.Version,
		Revision:       1,
		Graph:          cloneGraph(version.Graph),
		TriggeredBy:    WorkflowTriggeredByUser,
		ActivityID:     userActivityID,
		NodeStates:     map[string]string{},
		NodeOutputs:    map[string]map[string]any{},
		SelectedRoutes: map[string]string{},
		LoopState:      map[string]*LoopState{},
		LoopExitExpr:   map[string]string{},
	}
	if s.branch != nil {
		branch, err := s.branch(ctx, task, version.Graph)
		if err != nil {
			return err
		}
		run.IntegrationBranch = branch
	}
	if err := s.tasks.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		return err
	}
	if err := s.activities.SetWorkflowLink(ctx, agentAct.ID, run.ID, version.Version); err != nil {
		return err
	}
	agentAct.RunID = run.ID
	agentAct.WorkflowVersionID = version.Version
	s.broadcastUpdated(agentAct, map[string]any{
		"run_id":              run.ID,
		"workflow_version_id": version.Version,
	})
	if s.executor != nil {
		// The HTTP request returns before the run finishes; detach cancellation so
		// the turn does not die with the response writer.
		go s.executeWorkflowRun(context.WithoutCancel(ctx), task, run, agentAct.ID)
	}
	return nil
}

// executeWorkflowRun 只负责把占位回复推进到 running 并唤醒运行层。
// 终态不在这里写：唤醒是异步的，运行层会在真正结算时回写
// （Service.settleWorkflowReply），审批后继续跑也走同一条回写路径。
func (s *ActivityService) executeWorkflowRun(ctx context.Context, task Task, run *WorkflowRun, activityID string) {
	if s == nil || s.executor == nil {
		return
	}
	_ = s.activities.UpdateStatus(ctx, activityID, ActivityStatusRunning, nil)
	s.broadcastUpdatedByID(ctx, activityID, map[string]any{"status": ActivityStatusRunning})

	if err := s.executor.ExecuteActivityWorkflow(ctx, task, run); err != nil {
		_ = s.OnWorkflowRunFailed(ctx, run.ID, err.Error())
	}
}

// workflowProgressNode resolves a node's 1-based position and display name.
// Loop internals are flattened right after their container so nested progress
// still has a meaningful numerator.
func workflowProgressNode(run *WorkflowRun, nodeID string) (index, total int, name string, ok bool) {
	if run == nil {
		return 0, 0, "", false
	}
	nodes := make([]WorkflowNode, 0, len(run.Graph.Nodes))
	for _, n := range run.Graph.Nodes {
		nodes = append(nodes, n)
		if n.Kind != NodeKindLoop {
			continue
		}
		if inner, err := workflowLoopGraph(n); err == nil {
			nodes = append(nodes, inner.Nodes...)
		}
	}
	for i, n := range nodes {
		if n.ID != nodeID {
			continue
		}
		name = strings.TrimSpace(n.Name)
		if name == "" {
			name = n.ID
		}
		return i + 1, len(nodes), name, true
	}
	return 0, 0, "", false
}

// resolveWorkflowVersion picks the immutable version a run snapshots.
func (s *ActivityService) resolveWorkflowVersion(ctx context.Context, userInput, choice string, versionID *string) (WorkflowVersion, error) {
	if s.workflows == nil {
		return WorkflowVersion{}, errors.New("workflow control-plane store unavailable")
	}
	if choice == CommentWorkflowChoiceSpecified {
		if versionID == nil || strings.TrimSpace(*versionID) == "" {
			return WorkflowVersion{}, errors.New("workflow_version_id required when workflow_choice=specified")
		}
		id := strings.TrimSpace(*versionID)
		// 选择器列的是 workflow（id+name），只传 workflow id 时解析到最新发布版本；
		// 传已发布的版本 id 则原样使用。
		if version, err := s.workflows.GetVersion(ctx, id); err == nil {
			return version, nil
		}
		if latest, ok := s.latestVersion(ctx, id); ok {
			return s.workflows.GetVersion(ctx, latest)
		}
		return WorkflowVersion{}, ErrVersionNotFound
	}
	// empty = let the recommender choose. A recommendation failure must not block
	// the message: fall back to the first available workflow with a live version.
	if s.recommender != nil {
		if rec, err := s.recommender.Recommend(ctx, userInput); err == nil && rec != nil {
			if id, ok := s.latestVersion(ctx, rec.WorkflowID); ok {
				return s.workflows.GetVersion(ctx, id)
			}
		}
	}
	id, ok := s.defaultWorkflowVersion(ctx)
	if !ok {
		return WorkflowVersion{}, ErrNoAvailableWorkflows
	}
	return s.workflows.GetVersion(ctx, id)
}

// latestVersion returns the highest published sequence for a workflow id.
func (s *ActivityService) latestVersion(ctx context.Context, workflowID string) (string, bool) {
	versions, err := s.workflows.ListVersions(ctx)
	if err != nil {
		return "", false
	}
	best, bestSeq := "", -1
	for _, v := range versions {
		if v.WorkflowID != workflowID {
			continue
		}
		seq := workflowVersionSeq(v.Version)
		if seq > bestSeq {
			best, bestSeq = v.Version, seq
		}
	}
	return best, best != ""
}

// defaultWorkflowVersion is the plan's getDefaultWorkflowID: the first
// auto-select eligible workflow that actually has a published version.
func (s *ActivityService) defaultWorkflowVersion(ctx context.Context) (string, bool) {
	available, err := s.workflows.ListAvailable(ctx)
	if err != nil {
		return "", false
	}
	for _, wf := range available {
		if wf == nil {
			continue
		}
		if id, ok := s.latestVersion(ctx, wf.ID); ok {
			return id, true
		}
	}
	return "", false
}

// workflowVersionSeq parses the ":vN" suffix produced by PublishDraft.
func workflowVersionSeq(version string) int {
	idx := strings.LastIndex(version, ":v")
	if idx < 0 {
		return 0
	}
	n, err := strconv.Atoi(version[idx+2:])
	if err != nil {
		return 0
	}
	return n
}

// OnWorkflowRunFailed moves the agent reply linked to runID to failed and keeps
// the error visible in the summary (Review Focus #3).
func (s *ActivityService) OnWorkflowRunFailed(ctx context.Context, runID, errMsg string) error {
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		errMsg = "unknown error"
	}
	return s.finishByRunID(ctx, runID, ActivityStatusFailed, errMsg)
}

// OnWorkflowRunCompleted moves the agent reply linked to runID to completed.
func (s *ActivityService) OnWorkflowRunCompleted(ctx context.Context, runID, summary string) error {
	return s.finishByRunID(ctx, runID, ActivityStatusCompleted, summary)
}

func (s *ActivityService) finishByRunID(ctx context.Context, runID, status, summary string) error {
	if s == nil || s.activities == nil {
		return errors.New("activity service not configured")
	}
	activity, err := s.activities.ActivityByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if err := s.activities.UpdateStatus(ctx, activity.ID, status, &summary); err != nil {
		return err
	}
	activity.Status = status
	activity.Summary = summary
	s.broadcastUpdated(activity, map[string]any{"status": status, "summary": summary})
	return nil
}

// finishActivityByID 是直连路径的终态回写（直连没有 RunID）。
func (s *ActivityService) finishActivityByID(ctx context.Context, id, status, summary string) error {
	if s == nil || s.activities == nil {
		return errors.New("activity service not configured")
	}
	if err := s.activities.UpdateStatus(ctx, id, status, &summary); err != nil {
		return err
	}
	activity, err := s.activities.Get(ctx, id)
	if err != nil {
		return err
	}
	s.broadcastUpdated(activity, map[string]any{"status": status, "summary": summary})
	return nil
}

// failActivity writes a failure back onto the agent reply. Failures here are
// deliberate state, so the message survives even when the run cannot start.
func (s *ActivityService) failActivity(ctx context.Context, activity *TaskActivity, cause error) {
	summary := strings.TrimSpace(cause.Error())
	_ = s.activities.UpdateStatus(ctx, activity.ID, ActivityStatusFailed, &summary)
	activity.Status = ActivityStatusFailed
	activity.Summary = summary
	s.broadcastUpdated(activity, map[string]any{"status": ActivityStatusFailed, "summary": summary})
}

func normalizeWorkflowChoice(choice string) (string, error) {
	switch c := strings.TrimSpace(choice); c {
	case "":
		return CommentWorkflowChoiceEmpty, nil
	case CommentWorkflowChoiceEmpty, CommentWorkflowChoiceSpecified, CommentWorkflowChoiceDisabled:
		return c, nil
	default:
		return "", fmt.Errorf("invalid workflow_choice %q", choice)
	}
}

func (s *ActivityService) broadcastCreated(activity *TaskActivity) {
	if s != nil && s.broadcaster != nil {
		s.broadcaster.ActivityCreated(activity)
	}
}

func (s *ActivityService) broadcastUpdated(activity *TaskActivity, changes map[string]any) {
	if s != nil && s.broadcaster != nil {
		s.broadcaster.ActivityUpdated(activity, changes)
	}
}

func (s *ActivityService) broadcastUpdatedByID(ctx context.Context, activityID string, changes map[string]any) {
	activity, err := s.activities.Get(ctx, activityID)
	if err != nil {
		return
	}
	s.broadcastUpdated(activity, changes)
}
