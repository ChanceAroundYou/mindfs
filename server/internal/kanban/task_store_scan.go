package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (s *TaskStore) decorateCurrentStage(ctx context.Context, task *Task) {
	run, err := s.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return
	}
	task.CurrentStageName = run.StageName
	task.CurrentStageStatus = run.Status
}

type scanner interface{ Scan(dest ...any) error }

func scanTask(row scanner) (Task, error) {
	var task Task
	var createWorktree, askUserWaiting, hasPlan, hasTodos, hasTask int
	var worktreeBuilt int
	var sessionError string
	var labels, stagesJSON string
	var templateSnapshot string
	var created, updated string
	if err := row.Scan(&task.ID, &task.TaskNumber, &task.RootID, &task.TaskTemplateID, &task.TaskTemplateName, &templateSnapshot, &createWorktree, &task.WorktreeBranchMode, &task.WorktreeBranch, &task.CurrentStageIndex, &task.Status, &task.MainSessionKey, &task.WorktreeRootID, &task.WorktreePath, &worktreeBuilt, &askUserWaiting, &hasPlan, &hasTodos, &hasTask, &sessionError, &labels, &created, &updated, &task.CompletedAt, &task.Name, &stagesJSON); err != nil {
		return Task{}, err
	}
	task.CreateWorktree = createWorktree != 0
	// 存量回填之外还有一层兜底：老库里 worktree_built 可能还是 0，但路径非空说明
	// 树确实在。按「有路径就算建过」算，界面才不会把一棵活着的树说成没建过。
	task.WorktreeBuilt = worktreeBuilt != 0 || strings.TrimSpace(task.WorktreePath) != ""
	task.AuxFlags = TaskAuxFlags{
		AskUserWaiting: askUserWaiting != 0,
		HasPlan:        hasPlan != 0,
		HasTodos:       hasTodos != 0,
		HasTask:        hasTask != 0,
		SessionError:   strings.TrimSpace(sessionError),
	}
	_ = json.Unmarshal([]byte(labels), &task.Labels)
	if task.Stages == nil {
		task.Stages = []StageTemplate{}
	}
	if strings.TrimSpace(stagesJSON) != "" && stagesJSON != "[]" {
		_ = json.Unmarshal([]byte(stagesJSON), &task.Stages)
	}
	if task.Stages == nil {
		task.Stages = []StageTemplate{}
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return task, nil
}

func scanStageRun(row scanner) (StageRun, error) {
	var run StageRun
	var created, updated string
	if err := row.Scan(&run.ID, &run.TaskID, &run.StageIndex, &run.StageName, &run.Role, &run.Status, &run.SessionKey, &run.Input, &run.RenderedPrompt, &run.StartedAt, &run.FinishedAt, &created, &updated); err != nil {
		return StageRun{}, err
	}
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	run.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return run, nil
}

func scanTaskEvent(row scanner) (TaskEvent, error) {
	var event TaskEvent
	var created string
	if err := row.Scan(&event.ID, &event.TaskID, &event.StageRunID, &event.Type, &event.Payload, &created); err != nil {
		return TaskEvent{}, err
	}
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return event, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSuccess, StatusFail, StatusCancelled:
		return true
	default:
		return false
	}
}

// canLeaveStageOnRequest 报告用户**手动**点「下一段 / 立即执行」时能不能离开这一段。
//
// 与 canAdvanceFromStage 只差一处：agent 段的 waiting_user 在这里放行。
//
// 那个状态的字面含义是「agent 没输出 [STAGE-DONE:N]，也没说受阻」—— 是**没回报**，
// 不是活没干完。2026-10-03 实测（mindfs 任务 26）：定位段把方案讲完了，停在
// waiting_user，于是详情面板按 canAdvanceFromCurrentStage 不给按钮、看板按
// showAdvance 给了个点了没反应的按钮，任务彻底卡死 —— 而用户要做的恰恰是
// 「就这样，推进到修复段」。
//
// 放行安全的前提是**只有人点的路径走这里**：Next 只有 /api/tasks/{id}/next 这一个
// 入口（连 RunNow 的 waiting_user/pending 分支也是转调它），而引擎的自动推进走的是
// moveTo（auto_advanced 事件），根本不经过本函数。所以引擎依旧不会自己跳过没回报的段 ——
// 阶段错乱那条防线（TestAgentStageWithoutDoneMarkerStopsAtCurrentStage）不受影响。
// AddStage 也不经过这里：补一句评论顶不掉没走完的段，那条规则原样保留。
func canLeaveStageOnRequest(role, runStatus string) bool {
	if role == RoleAgent && runStatus == StageStatusWaitingUser {
		return true
	}
	return canAdvanceFromStage(role, runStatus)
}

// canAdvanceFromStage 报告是否允许离开由 role/runStatus 代表的这一段。
//
// 拦的是「跑过但没走完」：fail / cancelled / rejected。以前 moveRelative 只拦
// running，其余一律被 Next 当成「已批准」强行推进（曾被
// TestNextAdvancesFailedCurrentStageAfterUserReview 固化成期望行为），
// 于是前一段没干完，下一段就在错误前提上开跑，阶段错乱。
//
// role 必须一起看，两种「waiting_user」含义相反：
//   - user 段的 waiting_user 是「在等你的输入」，补一句评论就是答案，照常放行；
//   - agent 段的 waiting_user 是「agent 自己没回报完成」，停下等你，别当成答完。
//     引擎的自动推进因此也不走这条路；要放行只有用户手动点，见 canLeaveStageOnRequest。
//
// pending（还没跑过，首段等人批准）和 running（正在跑）另说：前者正是「用户批准
// 首段」这条正常流程，后者由调用方的 running 检查单独拦（并发推进会重复执行）。
func canAdvanceFromStage(role, runStatus string) bool {
	if role == RoleAgent {
		switch runStatus {
		case StageStatusPending, StageStatusRunning, StageStatusSuccess, StageStatusApproved:
			return true
		default:
			// fail / cancelled / rejected：agent 没走完，不许引擎自动推进。
			// waiting_user 同理 —— 但用户手动点「下一段」时走
			// canLeaveStageOnRequest，那里是放行的。
			return false
		}
	}
	// user 段：等的就是你，pending（等输入）与 waiting_user（还在等）都算可推进。
	switch runStatus {
	case StageStatusPending, StageStatusRunning, StageStatusWaitingUser,
		StageStatusApproved, StageStatusSuccess, StageStatusRejected:
		return true
	default:
		// fail / cancelled：user 段自己失败/被取消，不能靠一句评论跳过。
		return false
	}
}

func sortStageRunsDesc(items []StageRun) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

var errNoRows = sql.ErrNoRows

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
