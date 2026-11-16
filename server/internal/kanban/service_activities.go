package kanban

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Task 6 (Phase 2): per-root entry points for the conversation activity API.
//
// The plan's handler holds a single ActivityService/ActivityStore, but mindfs is
// multi-account and every task node lives in a per-root SQLite store. These
// helpers keep the HTTP layer free of store plumbing and give the (future)
// WebSocket wiring a single place to attach a broadcaster.

// ActivityServiceForRoot builds the ActivityService bound to one data-plane
// root. s.Workflows is the control-plane workflow store; the recommender
// degrades to the first available workflow when no LLM client is configured.
func (s *Service) ActivityServiceForRoot(rootID string) (*ActivityService, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	llm := s.workflowLLM
	s.mu.Unlock()
	svc := NewActivityService(store, s.Workflows, NewWorkflowRecommender(s.Workflows, llm))
	s.mu.Lock()
	broadcaster := s.activityBroadcaster
	s.mu.Unlock()
	svc.SetBroadcaster(broadcaster)
	svc.SetTurnTracker(&s.activityTurns)
	// 装配 WorkflowRun 执行端口：通过 Service.WakeWorkflowRun 触发实际执行
	svc.SetWorkflowExecutor(s)
	s.mu.Lock()
	direct := s.directExecutor
	s.mu.Unlock()
	svc.SetDirectExecutor(direct)
	// 装配合并分支解析器
	svc.SetBranchResolver(func(ctx context.Context, task Task, graph WorkflowDefinition) (string, error) {
		return s.snapshotRunIntegrationBranch(ctx, task, graph)
	})
	// D3 的执行入口守卫：同一时刻只有一个活动执行；停住的旧 Run 由下一条
	// workflow 留言整体取代。直连回合只做守卫，不冻结旧 Run。
	svc.SetRunLifecycle(s)
	// Task Router P0：mode=off 返回 nil，ActivityService 连一次额外读取都不做。
	svc.SetRouter(s.RouterServiceForRoot(rootID))
	return svc, nil
}

// WaitActivityTurnsIdle 等所有由本 Service 派发的异步对话回合退出。
// 只用于测试/停机收尾：先等回合（executeWorkflowRun）把 driver 装好，再
// WaitWorkflowRunIdle 才有确定的等待对象，不会在 TempDir 拆掉后继续写库。
func (s *Service) WaitActivityTurnsIdle(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		s.activityTurns.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetActivityBroadcaster 装配对话式活动流的广播端口（Task 7）。装配期调用一次；
// nil 表示不广播（单测/老装配行为不变）。
func (s *Service) SetActivityBroadcaster(b ActivityBroadcaster) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.activityBroadcaster = b
	s.mu.Unlock()
}

// SetWorkflowLLM 装配 workflow 自动选择的模型客户端。装配期调用一次；nil 时
// 推荐器按 id 顺序退回第一个可用 workflow（升级不配置时的兼容行为）。
func (s *Service) SetWorkflowLLM(client LLMClient) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.workflowLLM = client
	s.mu.Unlock()
}

// SetDirectExecutor 装配「纯对话模式」的执行端口（普通 Agent 回合）。
func (s *Service) SetDirectExecutor(e ActivityDirectExecutor) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.directExecutor = e
	s.mu.Unlock()
}

// broadcastWorkflowActivity 把 driver/store 在同一进程里写入的活动同步给任务对话。
// 活动可能由后台 driver 产生，HTTP 创建路径的广播不会经过它；漏掉这一步时，
// HumanGate 卡要等整页重拉才出现或消失。
func (s *Service) broadcastWorkflowActivity(ctx context.Context, store *TaskStore, activity *TaskActivity, created bool, changes map[string]any) {
	if s == nil || store == nil || activity == nil {
		return
	}
	svc, err := s.ActivityServiceForRoot(store.root.ID)
	if err != nil {
		return
	}
	if created {
		svc.broadcastCreated(activity)
		return
	}
	svc.broadcastUpdated(activity, changes)
}

// CreateUserActivity is the HTTP-facing entry point: it resolves the root's
// task store, then delegates to ActivityService.CreateUserActivity.
func (s *Service) CreateUserActivity(
	ctx context.Context,
	rootID, taskID, body, workflowChoice string,
	workflowVersionID *string,
) (*TaskActivity, *TaskActivity, error) {
	activityService, err := s.ActivityServiceForRoot(rootID)
	if err != nil {
		return nil, nil, err
	}
	return activityService.CreateUserActivity(ctx, taskID, body, workflowChoice, workflowVersionID)
}

// ListTaskActivities returns the conversation stream for one task. ActivityStore
// (not the legacy ListActivities projection) is used so status/summary survive.
func (s *Service) ListTaskActivities(ctx context.Context, rootID, taskID string) ([]*TaskActivity, error) {
	return s.ListTaskActivitiesWithHidden(ctx, rootID, taskID, false)
}

// ListTaskActivitiesWithHidden 在 includeHidden=true 时返回含隐藏行的完整流。
// 默认过滤由 ActivityStore 负责，这里只做任务归属校验。
func (s *Service) ListTaskActivitiesWithHidden(ctx context.Context, rootID, taskID string, includeHidden bool) ([]*TaskActivity, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	activities := NewActivityStore(store.db)
	if includeHidden {
		return activities.ListIncludingHidden(ctx, taskID)
	}
	return activities.List(ctx, taskID)
}

// GetTaskActivity loads one activity and verifies it belongs to taskID. The id
// alone is not a capability: crossing tasks within a root must still 404.
func (s *Service) GetTaskActivity(ctx context.Context, rootID, taskID, activityID string) (*TaskActivity, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	activity, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil {
		return nil, err
	}
	if activity.TaskID != strings.TrimSpace(taskID) {
		return nil, ErrActivityNotFound
	}
	return activity, nil
}

// SetTaskMainSessionKey 把纯对话模式建立的主会话记回任务。已有会话时幂等。
func (s *Service) SetTaskMainSessionKey(ctx context.Context, rootID, taskID, key string) error {
	store, err := s.taskStore(rootID)
	if err != nil {
		return err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || task.MainSessionKey == key {
		return nil
	}
	task.MainSessionKey = key
	task.UpdatedAt = time.Now().UTC()
	return store.UpdateTask(ctx, task)
}

// ExecuteActivityWorkflow implements ActivityWorkflowExecutor by triggering
// the workflow runtime driver (WakeWorkflowRun). 它只说「开始」，结算由
// projectWorkflowTaskStatus → settleWorkflowReply 在真实终态时完成。
func (s *Service) ExecuteActivityWorkflow(ctx context.Context, task Task, run *WorkflowRun) error {
	return s.WakeWorkflowRun(ctx, task.RootID, task.ID, "activity_created")
}

// updateWorkflowReplyProgress 把节点完成进度写到这一轮的 Agent 回复上
// （"步骤 i/n: 名称"）。进度是提示性的：任何失败都静默，不能影响运行。
func (s *Service) updateWorkflowReplyProgress(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeID string) {
	if s == nil || store == nil || run == nil {
		return
	}
	index, total, name, ok := workflowProgressNode(run, nodeID)
	if !ok {
		return
	}
	activity, err := NewActivityStore(store.db).ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	if activity.Status != ActivityStatusRunning {
		return
	}
	body := fmt.Sprintf("步骤 %d/%d: %s", index, total, name)
	if err := NewActivityStore(store.db).UpdateBody(ctx, activity.ID, body); err != nil {
		return
	}
	activity.Body = body
	s.broadcastActivityUpdated(activity, map[string]any{"body": body})
}

// settleWorkflowReply 把一轮运行的真实终态写回 Router 的占位回复。
//
// 触发点：任务状态投影（节点终态、HumanGate 之后的继续）与取消。只有任务
// 进入终态或 waiting_user 时才落；运行中保持 running。
func (s *Service) settleWorkflowReply(ctx context.Context, store *TaskStore, run *WorkflowRun) {
	if s == nil || store == nil || run == nil {
		return
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return
	}
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	status, summary := workflowReplyOutcome(ctx, store, run)
	if status == "" {
		return
	}
	activityChanged := !(status == activity.Status && (summary == "" || summary == activity.Summary))
	if activityChanged {
		if err := activities.UpdateStatus(ctx, activity.ID, status, &summary); err != nil {
			return
		}
		// 终态文案由 summary 提供；「步骤 i/n」是运行期提示，结算后就不成立了。
		_ = activities.ClearBody(ctx, activity.ID)
		activity.Status = status
		activity.Summary = summary
		activity.Body = ""
		s.broadcastActivityUpdated(activity, map[string]any{"status": status, "summary": summary, "body": ""})
	}
	// 任务行上的结果摘要供看板/工作台卡片展示；不再有活动状态要改时也要补写，
	// 否则升级前已结算的回复会让卡片永远停在原始需求上。
	if task.ResultSummary != summary {
		_ = store.UpdateTaskResultSummary(ctx, run.TaskID, summary)
	}
}

// backfillWorkflowReplySummaries 给升级前留下的空 summary 回复补一次可读结果。
// 只动 completed + run_id 非空 + summary 为空的行；settleWorkflowReply 内部按
// 当前任务状态重算，写入后该行不再命中查询，因此重复启动是幂等的。
func (s *Service) backfillWorkflowReplySummaries(ctx context.Context, store *TaskStore) {
	if s == nil || store == nil {
		return
	}
	activities, err := NewActivityStore(store.db).CompletedWorkflowRepliesWithoutSummary(ctx)
	if err != nil {
		return
	}
	for _, activity := range activities {
		if strings.TrimSpace(activity.RunID) == "" {
			continue
		}
		run, err := store.WorkflowRunByID(ctx, activity.RunID)
		if err != nil || run == nil {
			continue
		}
		s.settleWorkflowReply(ctx, store, run)
	}
}

// resumeWorkflowReply 把停在 waiting_user 的占位回复重新标成 running。
// 触发点：HumanGate 批准、暂停后恢复 —— 同一个 Run 继续跑，不新开回复。
func (s *Service) resumeWorkflowReply(ctx context.Context, store *TaskStore, taskID string) {
	if s == nil || store == nil {
		return
	}
	run, found, err := s.workflowRunForTask(ctx, store, taskID)
	if err != nil || !found || run == nil {
		return
	}
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	if activity.Status != ActivityStatusWaitingUser && activity.Status != ActivityStatusPending {
		return
	}
	if err := activities.UpdateStatus(ctx, activity.ID, ActivityStatusRunning, nil); err != nil {
		return
	}
	activity.Status = ActivityStatusRunning
	s.broadcastActivityUpdated(activity, map[string]any{"status": ActivityStatusRunning})
}

// workflowReplyOutcome maps the **run's** projection to the reply the Router
// shows the user. 用 Run 而不是任务状态，是因为成功的一轮会把任务停在
// waiting_user（待审核）：如果看任务状态，成功的回复会被写成 waiting_user，
// 用户永远看不到这一轮的结果。
//
// waiting_user 表示这一轮没结束、在等人；它不是失败，审批后同一个 Run 会继续。
//
// 这里只写「内容」，不写「壳」：状态徽标与占位文案由前端按 locale 渲染，
// 后端硬编码中文会让英文界面出现中英混排。
func workflowReplyOutcome(ctx context.Context, store *TaskStore, run *WorkflowRun) (string, string) {
	switch ProjectStatusFromRun(run) {
	case StatusSuccess:
		return ActivityStatusCompleted, workflowReplySummary(ctx, store, run)
	case StatusFail:
		return ActivityStatusFailed, workflowReplyReason(ctx, store, run)
	case StatusCancelled:
		return ActivityStatusCancelled, workflowReplyReason(ctx, store, run)
	case StatusWaitingUser:
		return ActivityStatusWaitingUser, workflowReplyReason(ctx, store, run)
	default:
		return "", ""
	}
}

// workflowReplySummary 取最后一个成功**Agent**节点输出里给人看的那段文字；
// 没有 Agent 产出就退回任意成功节点。条件/通知节点排在 Agent 后面很常见，
// 它们的输出是路由与簿记（{"route":"pass"}），不是用户要的结果。
// 绝不把 output 原样 JSON 出来当回复：那是节点之间的数据契约。
func workflowReplySummary(ctx context.Context, store *TaskStore, run *WorkflowRun) string {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return ""
	}
	kinds := workflowNodeKinds(run)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status != NodeRunSuccess || kinds[runs[i].NodeID] != NodeKindAgent {
			continue
		}
		if summary := nodeRunOutputSummary(runs[i]); summary != "" {
			return summary
		}
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status != NodeRunSuccess {
			continue
		}
		if summary := nodeRunOutputSummary(runs[i]); summary != "" {
			return summary
		}
	}
	return ""
}

// workflowNodeKinds 展开顶层节点与 Loop 内部节点，供按类型挑节点用。
func workflowNodeKinds(run *WorkflowRun) map[string]string {
	kinds := map[string]string{}
	if run == nil {
		return kinds
	}
	for _, node := range run.Graph.Nodes {
		kinds[node.ID] = node.Kind
		if node.Kind != NodeKindLoop {
			continue
		}
		if inner, err := workflowLoopGraph(node); err == nil {
			for _, innerNode := range inner.Nodes {
				kinds[innerNode.ID] = innerNode.Kind
			}
		}
	}
	return kinds
}

// workflowReplyReason 取最近一条非成功节点留下的可读原因（纯原因，不加前缀
// 或冒号）。NodeRun 本身不存 reason 列；能读的是节点输出里的 reason/prompt/summary。
func workflowReplyReason(ctx context.Context, store *TaskStore, run *WorkflowRun) string {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return ""
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == NodeRunSuccess || runs[i].Status == NodeRunQueued {
			continue
		}
		if reason := nodeRunText(runs[i], "reason", "error", "prompt", "summary"); reason != "" {
			return reason
		}
	}
	return ""
}

func nodeRunText(nr NodeRun, keys ...string) string {
	for _, key := range keys {
		value, _ := nr.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// nodeRunOutputMetadataKeys 是执行器写进 output 的簿记字段，不是节点产出内容。
// 它们参与「唯一字符串」判断会让真实正文永远被判为歧义（真实样本：envelope
// 的 outcome=done + 模型的 sentence 两把字符串，结果一条 summary 都发不出去）。
var nodeRunOutputMetadataKeys = map[string]bool{
	"outcome":               true,
	"reason":                true,
	"worktree":              true,
	"worktree_error":        true,
	"resumed_after_restart": true,
	"comment_id":            true,
	// 条件节点的选路结果。它是最常见的「后跑成功节点」，但不是任务产出。
	"route": true,
}

// nodeRunOutputSummary 从节点 output 里挑「给人看的那句」，顺序固定：
//  1. 显式 summary / message / text / result / title / detail；
//  2. 只剩一个文本字段 → 直接用它；
//  3. 多个文本字段 → 按 key 排序后渲染成「key: value；…」。真实模型常返回
//     conclusion/report/key_point 或 content/file 这类自由字段，没有 canonical
//     键；挑「最长」的一条会丢掉另一个要点，渲染键名则既不猜也不漏。
//
// 非文本值（数组/数字/对象）一律不参与：output 是节点间数据契约，转储出来只会
// 让用户看到内部结构。元数据键（outcome/reason/worktree_*）同理。
func nodeRunOutputSummary(nr NodeRun) string {
	if len(nr.Output) == 0 {
		return ""
	}
	for _, key := range []string{"summary", "message", "text", "result", "title", "detail"} {
		value, _ := nr.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return truncateSummary(trimmed)
		}
	}
	// 真实模型常把结果写成 changes: ["改了什么", ...]（任务 #66 的
	// workflow-blank 就是这样）。它不是节点间数据契约里的一个 key，而是
	// 这一轮给人看的变更清单，必须成为回复正文；否则用户只看到「已完成」。
	if changes := stringList(nr.Output["changes"]); len(changes) > 0 {
		lines := make([]string, len(changes))
		for i, change := range changes {
			lines[i] = "• " + change
		}
		return truncateSummary(strings.Join(lines, "\n"))
	}
	type textField struct{ key, value string }
	fields := make([]textField, 0, len(nr.Output))
	for key, value := range nr.Output {
		if nodeRunOutputMetadataKeys[key] {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			fields = append(fields, textField{key: key, value: trimmed})
		}
	}
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return truncateSummary(fields[0].value)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = field.key + ": " + field.value
	}
	return truncateSummary(strings.Join(parts, "；"))
}

// stringList 把 JSON 数组里的非空字符串按原顺序取出来；其它元素忽略。
// 兼容 json.Unmarshal 的 []any 与单测里直接写的 []string。
func stringList(value any) []string {
	var raw []any
	switch items := value.(type) {
	case []any:
		raw = items
	case []string:
		raw = make([]any, len(items))
		for i, item := range items {
			raw[i] = item
		}
	default:
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

const maxSummaryRunes = 1200

func truncateSummary(text string) string {
	runes := []rune(text)
	if len(runes) <= maxSummaryRunes {
		return text
	}
	return string(runes[:maxSummaryRunes]) + "…"
}

func (s *Service) broadcastActivityUpdated(activity *TaskActivity, changes map[string]any) {
	s.mu.Lock()
	b := s.activityBroadcaster
	s.mu.Unlock()
	if b != nil && activity != nil {
		b.ActivityUpdated(activity, changes)
	}
}
