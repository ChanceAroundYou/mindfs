package kanban

// 历史阶段任务的形状重建：把 task_stages_json / stage_runs 还原成图快照、节点运行、
// 活动与 Run，再落库。入口与事务管道在 task_store_convert_stages.go。
// 从该文件拆出（硬线 500，2026-11-22）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type legacyConversion struct {
	Task           legacyTaskRow
	Stages         []legacyStageTemplate
	LatestByIndex  map[int]legacyStageRun
	UserActivity   TaskActivity
	ReplyActivity  TaskActivity
	Run            *WorkflowRun
	NodeRuns       []NodeRun
	HasRunningNode bool
}

func buildLegacyConversion(task legacyTaskRow, runs []legacyStageRun) (legacyConversion, error) {
	stages := task.Stages
	if len(stages) == 0 {
		stages = synthesizeStagesFromRuns(runs)
	}
	latest := map[int]legacyStageRun{}
	for _, run := range runs {
		if current, ok := latest[run.StageIndex]; !ok || run.UpdatedAt.After(current.UpdatedAt) || run.CreatedAt.After(current.CreatedAt) {
			latest[run.StageIndex] = run
		}
	}

	userBody := strings.TrimSpace(latest[0].Input)
	if userBody == "" && len(stages) > 0 {
		userBody = strings.TrimSpace(stages[0].PromptTemplate)
	}
	if userBody == "" {
		userBody = strings.TrimSpace(task.Name)
	}
	if userBody == "" {
		userBody = "历史任务（无输入记录）"
	}
	userAt := task.CreatedAt
	if run, ok := latest[0]; ok && !run.CreatedAt.IsZero() {
		userAt = run.CreatedAt
	}
	conv := legacyConversion{
		Task:          task,
		Stages:        stages,
		LatestByIndex: latest,
		UserActivity: TaskActivity{
			ID: legacyUserActivityPrefix + task.ID, TaskID: task.ID, RootID: task.RootID,
			Type: ActivityTypeUserMessage, Author: "user", Role: RoleUser, Body: userBody,
			WorkflowChoice: CommentWorkflowChoiceEmpty, CreatedAt: userAt, UpdatedAt: userAt,
		},
	}

	graph, nodeRuns, hasRunning := legacyGraphFromStages(task, stages, latest)
	replyAt := legacyReplyTime(task, latest, userAt)
	conv.ReplyActivity = TaskActivity{
		ID: legacyReplyActivityPrefix + task.ID, TaskID: task.ID, RootID: task.RootID,
		Type: ActivityTypeAgentReply, Author: "agent", Role: RoleAgent,
		Body: "", WorkflowChoice: CommentWorkflowChoiceEmpty,
		Status: activityStatusFromTask(task.Status), Summary: legacySummary(task, runs),
		SessionKey: legacySessionKey(task, runs), CreatedAt: replyAt, UpdatedAt: replyAt,
	}
	if len(graph.Nodes) > 0 {
		runID := legacyRunPrefix + task.ID
		conv.ReplyActivity.RunID = runID
		conv.Run = &WorkflowRun{
			ID: runID, TaskID: task.ID, Version: legacyWorkflowVersionFor(task.TaskTemplateName),
			Revision: 1, Graph: graph,
			IntegrationBranch: strings.TrimSpace(task.WorktreeBranch),
			TriggeredBy:       WorkflowTriggeredByLegacyImport,
			ActivityID:        conv.UserActivity.ID,
			NodeStates:        map[string]string{},
			NodeOutputs:       map[string]map[string]any{},
			SelectedRoutes:    map[string]string{},
			LoopState:         map[string]*LoopState{},
			LoopExitExpr:      map[string]string{},
			CreatedAt:         userAt,
			UpdatedAt:         replyAt,
		}
		for nodeID, state := range graphNodeStates(graph, latest) {
			conv.Run.NodeStates[nodeID] = state
		}
		for i := range nodeRuns {
			nodeRuns[i].RunID = runID
		}
		conv.NodeRuns = nodeRuns
		conv.HasRunningNode = hasRunning
	}
	return conv, nil
}

func synthesizeStagesFromRuns(runs []legacyStageRun) []legacyStageTemplate {
	byIndex := map[int]legacyStageRun{}
	for _, run := range runs {
		if current, ok := byIndex[run.StageIndex]; !ok || run.UpdatedAt.After(current.UpdatedAt) || run.CreatedAt.After(current.CreatedAt) {
			byIndex[run.StageIndex] = run
		}
	}
	maxIndex := -1
	for index := range byIndex {
		if index > maxIndex {
			maxIndex = index
		}
	}
	stages := make([]legacyStageTemplate, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		run := byIndex[i]
		role := run.Role
		if role == "" {
			role = RoleAgent
		}
		stages = append(stages, legacyStageTemplate{
			ID: run.ID, Name: run.StageName, Role: role, PromptTemplate: run.RenderedPrompt,
		})
	}
	return stages
}

func legacyGraphFromStages(task legacyTaskRow, stages []legacyStageTemplate, latest map[int]legacyStageRun) (WorkflowDefinition, []NodeRun, bool) {
	graph := WorkflowDefinition{RootID: task.RootID}
	var nodeRuns []NodeRun
	hasRunning := false
	previous := ""
	for index := 1; index < len(stages); index++ {
		stage := stages[index]
		nodeID := fmt.Sprintf("stage-%d", index)
		node := WorkflowNode{ID: nodeID, Name: legacyStageName(stage, index)}
		run, hasRun := latest[index]
		prompt := strings.TrimSpace(stage.PromptTemplate)
		if hasRun && strings.TrimSpace(run.RenderedPrompt) != "" {
			prompt = strings.TrimSpace(run.RenderedPrompt)
		}
		if hasRun && prompt == "" {
			prompt = strings.TrimSpace(run.Input)
		}
		if stage.Role == RoleUser {
			node.Kind = NodeKindHumanGate
			node.Params = map[string]any{"prompt": prompt}
		} else {
			node.Kind = NodeKindAgent
			node.Params = map[string]any{
				"agent":            strings.TrimSpace(stage.Agent),
				"model":            strings.TrimSpace(stage.Model),
				"prompt":           prompt,
				"writes_workspace": true,
			}
		}
		graph.Nodes = append(graph.Nodes, node)
		if previous != "" {
			graph.Edges = append(graph.Edges, WorkflowEdge{From: previous, To: nodeID})
		}
		previous = nodeID

		status := NodeRunPending
		if hasRun {
			status = legacyNodeStatus(run.Status)
		}
		if status == NodeRunRunning {
			hasRunning = true
		}
		nr := NodeRun{
			ID: legacyNodeRunPrefix + task.ID + "-" + fmt.Sprint(index),
			// RunID 由调用方补；node 级证据保留原始 prompt 与会话。
			NodeID: nodeID, Attempt: 1, Status: status, Agent: stage.Agent, Model: stage.Model,
			Input:  map[string]any{"prompt": prompt, "stage_index": index, "stage_name": node.Name},
			Output: map[string]any{},
		}
		if hasRun {
			nr.SessionKey = run.SessionKey
			nr.StartedAt = parseLegacyTime(run.StartedAt)
			nr.FinishedAt = parseLegacyTime(run.FinishedAt)
			if nr.StartedAt.IsZero() && status != NodeRunPending {
				nr.StartedAt = run.CreatedAt
			}
			if nr.FinishedAt.IsZero() && isLegacyTerminalNodeStatus(status) {
				nr.FinishedAt = run.UpdatedAt
			}
		}
		nodeRuns = append(nodeRuns, nr)
	}
	return graph, nodeRuns, hasRunning
}

func graphNodeStates(graph WorkflowDefinition, latest map[int]legacyStageRun) map[string]string {
	states := map[string]string{}
	for index := 1; index < len(graph.Nodes)+1; index++ {
		nodeID := fmt.Sprintf("stage-%d", index)
		if _, ok := graph.node(nodeID); !ok {
			continue
		}
		state := NodeRunPending
		if run, ok := latest[index]; ok {
			state = legacyNodeStatus(run.Status)
		}
		states[nodeID] = state
	}
	return states
}

func legacyStageName(stage legacyStageTemplate, index int) string {
	if name := strings.TrimSpace(stage.Name); name != "" {
		return name
	}
	return fmt.Sprintf("阶段 %d", index+1)
}

func legacyNodeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case legacyStageStatusSuccess, legacyStageStatusApproved:
		return NodeRunSuccess
	case legacyStageStatusFail:
		return NodeRunFail
	case legacyStageStatusCancelled, legacyStageStatusRejected:
		return NodeRunCancelled
	case legacyStageStatusWaitingUser:
		return NodeRunWaitingUser
	case legacyStageStatusRunning:
		return NodeRunRunning
	default:
		return NodeRunPending
	}
}

func isLegacyTerminalNodeStatus(status string) bool {
	switch status {
	case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunWaitingUser, NodeRunUnknown:
		return true
	default:
		return false
	}
}

func activityStatusFromTask(status string) string {
	switch status {
	case StatusSuccess:
		return ActivityStatusCompleted
	case StatusFail:
		return ActivityStatusFailed
	case StatusCancelled:
		return ActivityStatusCancelled
	case StatusRunning:
		return ActivityStatusRunning
	default:
		return ActivityStatusPending
	}
}

func legacySummary(task legacyTaskRow, runs []legacyStageRun) string {
	total := len(task.Stages)
	if total == 0 {
		total = len(runs)
	}
	completed := 0
	for _, run := range runs {
		if run.Status == legacyStageStatusSuccess || run.Status == legacyStageStatusApproved {
			completed++
		}
	}
	count := fmt.Sprintf("%d/%d 个阶段", completed, total)
	switch task.Status {
	case StatusSuccess:
		if total == 0 {
			return "任务已完成（无详细记录）"
		}
		return "已完成 " + count
	case StatusFail:
		return "任务失败（已完成 " + count + "）"
	case StatusCancelled:
		return "任务已取消（已完成 " + count + "）"
	case StatusRunning:
		return "正在执行（已完成 " + count + "）"
	case StatusWaitingUser:
		return "等待你的输入（已完成 " + count + "）"
	default:
		if total == 0 {
			return "待开始"
		}
		return "待开始（共 " + fmt.Sprint(total) + " 个阶段）"
	}
}

func legacySessionKey(task legacyTaskRow, runs []legacyStageRun) string {
	if key := strings.TrimSpace(task.MainSessionKey); key != "" {
		return key
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if key := strings.TrimSpace(runs[i].SessionKey); key != "" {
			return key
		}
	}
	return ""
}

func legacyReplyTime(task legacyTaskRow, latest map[int]legacyStageRun, fallback time.Time) time.Time {
	if completed := parseLegacyTime(task.CompletedAt); !completed.IsZero() {
		return laterTime(completed, fallback)
	}
	var newest time.Time
	for _, run := range latest {
		if run.UpdatedAt.After(newest) {
			newest = run.UpdatedAt
		}
		if finished := parseLegacyTime(run.FinishedAt); finished.After(newest) {
			newest = finished
		}
	}
	if newest.IsZero() {
		newest = task.UpdatedAt
	}
	if newest.Before(fallback) {
		newest = fallback
	}
	return newest
}

func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func parseLegacyTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

// legacyWorkflowVersionFor 把历史模板名映射到**已发布**的 workflow 版本。
// 2026-11-12 的模板迁移已经把三个内置模板转成 workflow-new-feature /
// workflow-debug / workflow-blank 并发布为 v1，所以这里引用同一批版本，
// 让 provenance 可解析、不再造一批只在历史里存在的 workflow-legacy-*。
// 映射不上的模板（自定义/backlog/空）返回空串：Run 的图快照是内联的，
// 运行视图不依赖版本号，宁可留空也不谎报来源。
func legacyWorkflowVersionFor(templateName string) string {
	name := strings.TrimSpace(templateName)
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(name, "新功能"):
		return "workflow-new-feature:v1"
	case strings.Contains(lower, "debug"), strings.Contains(name, "调试"):
		return "workflow-debug:v1"
	case strings.Contains(name, "空白"):
		return "workflow-blank:v1"
	default:
		return ""
	}
}

func insertLegacyActivitiesTx(ctx context.Context, tx *sql.Tx, conv legacyConversion) error {
	for _, activity := range []TaskActivity{conv.UserActivity, conv.ReplyActivity} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_activities (id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			activity.ID, activity.TaskID, activity.RootID, activity.Type, activity.Author, activity.Role,
			activity.Body, activity.WorkflowChoice, activity.WorkflowVersionID, activity.Status,
			activity.Summary, activity.RunID, activity.NodeRunID, activity.SessionKey,
			activity.CreatedAt.UTC().Format(time.RFC3339Nano), activity.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func insertLegacyRunTx(ctx context.Context, tx *sql.Tx, conv legacyConversion) error {
	run := conv.Run
	graph, err := json.Marshal(run.Graph)
	if err != nil {
		return err
	}
	states, err := json.Marshal(run.NodeStates)
	if err != nil {
		return err
	}
	outputs, err := json.Marshal(run.NodeOutputs)
	if err != nil {
		return err
	}
	routes, err := json.Marshal(run.SelectedRoutes)
	if err != nil {
		return err
	}
	loops, err := json.Marshal(run.LoopState)
	if err != nil {
		return err
	}
	exits, err := json.Marshal(run.LoopExitExpr)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workflow_runs (id, task_id, version, revision, graph_json, node_states_json, node_outputs_json, selected_routes_json, loop_state_json, loop_exit_expr_json, created_at, updated_at, integration_branch, triggered_by, activity_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET version=excluded.version, graph_json=excluded.graph_json, node_states_json=excluded.node_states_json, node_outputs_json=excluded.node_outputs_json, selected_routes_json=excluded.selected_routes_json, loop_state_json=excluded.loop_state_json, loop_exit_expr_json=excluded.loop_exit_expr_json, updated_at=excluded.updated_at, integration_branch=excluded.integration_branch, triggered_by=excluded.triggered_by, activity_id=excluded.activity_id`,
		run.ID, run.TaskID, run.Version, run.Revision, string(graph), string(states), string(outputs), string(routes), string(loops), string(exits),
		run.CreatedAt.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano),
		run.IntegrationBranch, run.TriggeredBy, run.ActivityID); err != nil {
		return err
	}
	for _, nr := range conv.NodeRuns {
		input, err := json.Marshal(nr.Input)
		if err != nil {
			return err
		}
		output, err := json.Marshal(nr.Output)
		if err != nil {
			return err
		}
		started, finished := "", ""
		if !nr.StartedAt.IsZero() {
			started = nr.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if !nr.FinishedAt.IsZero() {
			finished = nr.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		created := run.CreatedAt
		if !nr.StartedAt.IsZero() {
			created = nr.StartedAt
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_node_runs (id, run_id, node_id, loop_path, iteration, attempt, agent, harness, model, session_key, status, input_json, output_json, started_at, finished_at, prev_attempt_id, worktree_id, worktree_path, worktree_branch, created_at, updated_at)
			 VALUES (?, ?, ?, '', 0, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, '', '', '', '', ?, ?)
			 ON CONFLICT(id) DO UPDATE SET status=excluded.status, session_key=excluded.session_key, input_json=excluded.input_json, output_json=excluded.output_json, started_at=excluded.started_at, finished_at=excluded.finished_at, updated_at=excluded.updated_at`,
			nr.ID, nr.RunID, nr.NodeID, nr.Attempt, nr.Agent, nr.Model, nr.SessionKey, nr.Status, string(input), string(output), started, finished,
			created.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}
