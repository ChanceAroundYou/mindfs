package kanban

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
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

// ActivityRunLifecycle 是「新留言开始新一轮」的状态机端口。
//
// 它把 D3「每任务至多一个非终态 Run / 一个活动执行」落到执行入口：直接建新 Run
// 会被 task_execution_engines 的绑定守卫拒绝，或者建成一条任务已处于终态、driver
// 永不推进的僵尸 Run。supersede=false 时只做守卫（直连回合不冻结旧 Run）；
// supersede=true 时把停在 waiting_user 的旧 Run 冻结为 superseded 并复位任务状态。
type ActivityRunLifecycle interface {
	PrepareNewUserTurn(ctx context.Context, task Task, supersede bool) error
}

// ActivityNodeAnswerer 是「把用户答复交回停住的节点」的执行端口：重开一次
// 尝试、带着答复重跑，并返回新的 NodeRun（成功回执要说出答到了哪个节点）。
type ActivityNodeAnswerer interface {
	AnswerWaitingWorkflowNode(ctx context.Context, rootID, taskID, nodeRunID, body, activityID string) (NodeRun, error)
}

type ActivityService struct {
	tasks          *TaskStore
	activities     *ActivityStore
	workflows      *WorkflowStore
	recommender    *WorkflowRecommender
	broadcaster    ActivityBroadcaster
	executor       ActivityWorkflowExecutor
	directExecutor ActivityDirectExecutor
	branch         ActivityBranchResolver
	lifecycle      ActivityRunLifecycle
	nodeAnswerer   ActivityNodeAnswerer
	// router 是 shadow 观察端口。nil（单测/老装配）时行为与改造前完全一致；
	// shadow 只读、只记账，不改变本函数的任何返回值或副作用。
	router *RouterService
	// turnWG 跟踪本服务派发的异步回合，供测试/停机收尾等待。nil 时不跟踪。
	// 每轮的 Add 都在启动 goroutine 之前同步完成，Wait 不会与 Add 抢跑。
	turnWG *sync.WaitGroup
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

// SetRouter 装配 shadow 观察端口。Mode=off 时调用方传 nil，本服务不做任何
// 额外读取，旧路径零变化。
func (s *ActivityService) SetRouter(router *RouterService) {
	if s != nil {
		s.router = router
	}
}

// SetTurnTracker 让调用方等待本服务派发的异步回合退出（测试收尾/停机用）。
// 装配期调用一次；nil 时回合不登记。
func (s *ActivityService) SetTurnTracker(wg *sync.WaitGroup) {
	if s != nil {
		s.turnWG = wg
	}
}

// SetRunLifecycle 装配「新一轮留言」的状态机端口。装配期调用一次；nil 时
// 维持历史行为（不守 D3，由 Run 创建守卫兜底）。
func (s *ActivityService) SetRunLifecycle(l ActivityRunLifecycle) {
	if s != nil {
		s.lifecycle = l
	}
}

// SetNodeAnswerer 装配「答复停住的节点」端口。装配期调用一次；nil 时该动作
// 明确失败（而不是假装成功），占位回复会显示原因。
func (s *ActivityService) SetNodeAnswerer(a ActivityNodeAnswerer) {
	if s != nil {
		s.nodeAnswerer = a
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
	if strings.TrimSpace(body) == "" {
		return nil, nil, ErrEmptyActivityBody
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	choice, err := normalizeWorkflowChoice(workflowChoice)
	if err != nil {
		return nil, nil, err
	}
	userAct, err := s.insertUserActivity(ctx, task, body, choice, "")
	if err != nil {
		return nil, nil, err
	}
	return s.dispatchUserActivity(ctx, task, userAct, workflowVersionID)
}

// QueueUserActivity 落一条「先不执行」的用户留言（新建任务勾掉「立即执行」）。
//
// 与 CreateUserActivity 的区别只在**不派发**：留言以 queued 状态进入对话流，
// 任务停在「待开始」；之后 RunNow 调 DispatchQueuedUserActivity 再交给 Router。
func (s *ActivityService) QueueUserActivity(ctx context.Context, taskID, body string) (*TaskActivity, error) {
	if s == nil || s.tasks == nil || s.activities == nil {
		return nil, errors.New("activity service not configured")
	}
	if strings.TrimSpace(body) == "" {
		return nil, ErrEmptyActivityBody
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.insertUserActivity(ctx, task, body, CommentWorkflowChoiceEmpty, ActivityStatusQueued)
}

// DispatchQueuedUserActivity 把最早一条排队留言交给 Router（RunNow 的实现）。
// 没有排队留言时是幂等空操作；两个「立即执行」同时到达时只有一个能领走留言。
func (s *ActivityService) DispatchQueuedUserActivity(ctx context.Context, taskID string) (*TaskActivity, *TaskActivity, error) {
	if s == nil || s.tasks == nil || s.activities == nil {
		return nil, nil, errors.New("activity service not configured")
	}
	taskID = strings.TrimSpace(taskID)
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	userAct, err := s.activities.ClaimFirstQueuedUserMessage(ctx, taskID)
	if errors.Is(err, ErrActivityNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	s.broadcastUpdated(userAct, map[string]any{"status": ""})
	return s.dispatchUserActivity(ctx, task, userAct, nil)
}

// insertUserActivity 校验并落一条用户留言，统一负责广播。
// status 只有排队的首条留言用 queued，其余留言留空。
func (s *ActivityService) insertUserActivity(
	ctx context.Context,
	task Task,
	body string,
	choice string,
	status string,
) (*TaskActivity, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, ErrEmptyActivityBody
	}
	userAct := &TaskActivity{
		ID:             newID("act"),
		TaskID:         task.ID,
		RootID:         task.RootID,
		Type:           ActivityTypeUserMessage,
		Role:           RoleUser,
		Body:           body,
		WorkflowChoice: choice,
		Status:         status,
	}
	if err := s.activities.Insert(ctx, userAct); err != nil {
		return nil, err
	}
	s.broadcastCreated(userAct)
	return userAct, nil
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
