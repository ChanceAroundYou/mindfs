package kanban

import (
	"context"
	"errors"
	"strings"
)

// runTurn 异步执行一轮对话并登记生命周期。Add 必须在 go 之前同步完成：
// 否则等待方可能先看到计数为 0，随后回合才启动，收尾时又和它抢同一份临时库。
func (s *ActivityService) runTurn(ctx context.Context, fn func(context.Context)) {
	ctx = context.WithoutCancel(ctx)
	if s.turnWG != nil {
		s.turnWG.Add(1)
	}
	go func() {
		if s.turnWG != nil {
			defer s.turnWG.Done()
		}
		fn(ctx)
	}()
}

// newAgentActivity 造一条 agent 回复占位行（未落库）。choice 决定旧路径把它
// 当作「纯对话」还是某个 workflow 的回复。
func (s *ActivityService) newAgentActivity(task Task, choice string) *TaskActivity {
	return &TaskActivity{
		ID:             newID("act"),
		TaskID:         task.ID,
		RootID:         task.RootID,
		Type:           ActivityTypeAgentReply,
		Role:           RoleAgent,
		Body:           "",
		Status:         ActivityStatusPending,
		WorkflowChoice: choice,
	}
}

// ensureAgentActivity 复用调用方已同步落库的占位回复；没有时才新插一条。
// Route 模式必须先有占位（用户点发送立刻能看到「正在想」），回落路径必须复用
// 同一条，否则一次留言会出现两条 agent 回复。
func (s *ActivityService) ensureAgentActivity(ctx context.Context, task Task, choice string, placeholder *TaskActivity) (*TaskActivity, error) {
	if placeholder != nil {
		return placeholder, nil
	}
	agentAct := s.newAgentActivity(task, choice)
	if err := s.activities.Insert(ctx, agentAct); err != nil {
		return nil, err
	}
	s.broadcastCreated(agentAct)
	return agentAct, nil
}

// beginDirectAgent 是「纯对话模式」的一轮：占位回复已落库（或新插），把这一轮
// 交给直连执行端口。没有端口就显式失败，绝不留下永久 pending。
func (s *ActivityService) beginDirectAgent(ctx context.Context, task Task, body string, placeholder *TaskActivity, choice string) (*TaskActivity, error) {
	agentAct, err := s.ensureAgentActivity(ctx, task, choice, placeholder)
	if err != nil {
		return nil, err
	}
	// 直连只守 D3（没有活动执行），不冻结停在 waiting_user 的旧 Run。
	if guardErr := s.prepareUserTurn(ctx, task, false, agentAct); guardErr != nil {
		return agentAct, nil
	}
	if s.directExecutor == nil {
		s.failActivity(ctx, agentAct, errors.New("direct executor not configured"))
		return agentAct, nil
	}
	s.runTurn(ctx, func(ctx context.Context) {
		s.executeDirectActivity(ctx, agentAct, task, body)
	})
	return agentAct, nil
}

// prepareRouterTurn 允许协调正在执行的节点，任务暂停、结束和其他错误仍立即反馈。
func (s *ActivityService) prepareRouterTurn(ctx context.Context, task Task, placeholder *TaskActivity) error {
	if s.lifecycle == nil {
		return nil
	}
	err := s.lifecycle.PrepareNewUserTurn(ctx, task, false)
	if err == nil || errors.Is(err, ErrWorkflowTurnActive) {
		return nil
	}
	s.failActivity(ctx, placeholder, err)
	return err
}

// prepareUserTurn 把「新一轮开始」的守卫/取代交给状态机端口。失败只写回这一轮
// 的占位回复：用户留言已经落库，不能因为拒绝而消失。
func (s *ActivityService) prepareUserTurn(ctx context.Context, task Task, supersede bool, agentAct *TaskActivity) error {
	if s == nil || s.lifecycle == nil {
		return nil
	}
	if err := s.lifecycle.PrepareNewUserTurn(ctx, task, supersede); err != nil {
		s.failActivity(ctx, agentAct, err)
		return err
	}
	return nil
}

// executeDirectActivity 跑一轮普通对话并回写占位回复。
// 正文只写 Agent 的真实输出：运行中/无输出/失败这些「壳」由前端按 locale 渲染。
//
// 直连回合没有 WorkflowRun 可以投影任务状态：只改活动状态的话任务会一直停在
// pending，界面左上角就会「明明在执行还是待开始」。回合开始时把任务投影为
// running，结束后恢复空闲（pending / 原来的 waiting_user）。
func (s *ActivityService) executeDirectActivity(ctx context.Context, agentAct *TaskActivity, task Task, body string) {
	restore := s.beginTurnTaskStatus(ctx, task.ID)
	defer restore()

	_ = s.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil)
	s.broadcastUpdatedByID(ctx, agentAct.ID, map[string]any{"status": ActivityStatusRunning})

	text, err := s.directExecutor.RunDirectActivity(ctx, task, body)
	if err != nil {
		failure := strings.TrimSpace(err.Error())
		_ = s.activities.UpdateBody(ctx, agentAct.ID, failure)
		// 先恢复任务状态再广播终态：前端收到 activity.updated 会立刻点查任务头，
		// 晚于广播的恢复会让界面停在「执行中」。
		restore()
		s.recordTurnResultSummary(ctx, task.ID, failure)
		_ = s.finishActivityByID(ctx, agentAct.ID, ActivityStatusFailed, failure)
		return
	}
	_ = s.activities.UpdateBody(ctx, agentAct.ID, strings.TrimSpace(text))
	restore()
	s.recordTurnResultSummary(ctx, task.ID, text)
	_ = s.finishActivityByID(ctx, agentAct.ID, ActivityStatusCompleted, "")
}

// recordTurnResultSummary 把这一轮的结果写回任务行，供看板卡片展示。直连回合
// 没有 WorkflowRun 可投影，回复正文就是结果；不写的话卡片只能看到用户原始需求。
func (s *ActivityService) recordTurnResultSummary(ctx context.Context, taskID, text string) {
	if s == nil || s.tasks == nil {
		return
	}
	_ = s.tasks.UpdateTaskResultSummary(ctx, taskID, truncateSummary(strings.TrimSpace(text)))
}

// beginWorkflowRun 落一个 WorkflowRun 并把它挂到占位回复上。
//
// Run 创建失败不向上抛：用户留言已经落库，把失败写回 Agent 回复（status=failed
// + error summary）比让整条留言消失更符合对话模型。真正的输入错误（空留言、
// 非法 choice）在调用方已经提前拒绝。
func (s *ActivityService) beginWorkflowRun(
	ctx context.Context,
	task Task,
	userInput string,
	choice string,
	versionID *string,
	userActivityID string,
	placeholder *TaskActivity,
) (*TaskActivity, error) {
	agentAct, err := s.ensureAgentActivity(ctx, task, choice, placeholder)
	if err != nil {
		return nil, err
	}
	// workflow 回合除了守 D3，还要把停在 waiting_user 的旧 Run 冻结掉：
	// 否则新建 Run 会被绑定守卫拒绝，或者建成一条 driver 永不推进的僵尸 Run。
	if guardErr := s.prepareUserTurn(ctx, task, true, agentAct); guardErr != nil {
		return agentAct, nil
	}
	version, err := s.resolveWorkflowVersion(ctx, userInput, choice, versionID)
	if err == nil {
		_, err = s.persistWorkflowRun(ctx, task, version, userActivityID, agentAct)
	}
	if err != nil {
		s.failActivity(ctx, agentAct, err)
	}
	return agentAct, nil
}

// beginWorkflowRunVersion 是 route 的 run_workflow：版本已由决策指定（或已
// 解析成最新版），不再经过 recommender 重新挑 workflow。
func (s *ActivityService) beginWorkflowRunVersion(
	ctx context.Context,
	task Task,
	version WorkflowVersion,
	userActivityID string,
	placeholder *TaskActivity,
) (*TaskActivity, error) {
	agentAct, err := s.ensureAgentActivity(ctx, task, CommentWorkflowChoiceEmpty, placeholder)
	if err != nil {
		return nil, err
	}
	if guardErr := s.prepareUserTurn(ctx, task, true, agentAct); guardErr != nil {
		return agentAct, nil
	}
	if _, err := s.persistWorkflowRun(ctx, task, version, userActivityID, agentAct); err != nil {
		s.failActivity(ctx, agentAct, err)
	}
	return agentAct, nil
}

// persistWorkflowRun creates the data-plane run, links it to the agent activity,
// and (when an executor is wired) starts asynchronous execution.
func (s *ActivityService) persistWorkflowRun(ctx context.Context, task Task, version WorkflowVersion, userActivityID string, agentAct *TaskActivity) (*WorkflowRun, error) {
	if s.tasks == nil {
		return nil, errors.New("task store unavailable")
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
			return nil, err
		}
		run.IntegrationBranch = branch
	}
	if err := s.tasks.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		return nil, err
	}
	if err := s.activities.SetWorkflowLink(ctx, agentAct.ID, run.ID, version.Version); err != nil {
		return nil, err
	}
	agentAct.RunID = run.ID
	agentAct.WorkflowVersionID = version.Version
	s.broadcastUpdated(agentAct, map[string]any{
		"run_id":              run.ID,
		"workflow_version_id": version.Version,
	})
	if s.executor != nil {
		// The HTTP request returns before the run finishes; the turn is detached
		// from the response writer but still tracked for test/shutdown waits.
		s.runTurn(ctx, func(ctx context.Context) {
			s.executeWorkflowRun(ctx, task, run, agentAct.ID)
		})
	}
	return run, nil
}

// executeWorkflowRun 只负责把占位回复推进到 running 并唤醒运行层。
// 终态不在这里写：唤醒是异步的，运行层会在真正结算时回写
// （Service.settleWorkflowReply），审批后继续跑也走同一条回写路径。
func (s *ActivityService) executeWorkflowRun(ctx context.Context, task Task, run *WorkflowRun, activityID string) {
	if s == nil || s.executor == nil {
		return
	}
	// 结算可能先于启动 goroutine 落库；running 不能覆盖终态，否则重连后
	// 这条回复会一直停在「执行中」。
	started, err := s.activities.MarkRunningIfActive(ctx, activityID)
	if err != nil {
		// 状态读写在 SQLite 锁竞争下可能短暂失败；启动本身不能被它挡住。
		_ = s.activities.UpdateStatus(ctx, activityID, ActivityStatusRunning, nil)
		started = true
	}
	if !started {
		return
	}
	// 回合已经进入执行（Router 决策、等待首个节点都算），任务头不能还写待开始。
	s.markTurnTaskRunning(ctx, task.ID)
	s.broadcastUpdatedByID(ctx, activityID, map[string]any{"status": ActivityStatusRunning})

	if err := s.executor.ExecuteActivityWorkflow(ctx, task, run); err != nil {
		_ = s.OnWorkflowRunFailed(ctx, run.ID, err.Error())
	}
}
