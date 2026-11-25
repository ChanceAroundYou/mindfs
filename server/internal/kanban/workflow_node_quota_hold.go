package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// supervisionMaxHoldsPerRun 是同一轮 Run 允许的配额挂起次数。配额挂起本身不改变
// 任务状态（它会自己到点继续），但上游一直不给额度时不能永远「等 15 分钟再试」：
// 额度用完说明这件事已经需要人介入，才转 waiting_user。
const supervisionMaxHoldsPerRun = 3

// ErrQuotaHoldsExhausted 是「这一轮 Run 的配额挂起额度已用完」的类型化拒绝。
// 调用方（自动补做、监管动作）据此区分别的失败，不要把它当普通错误重试。
var ErrQuotaHoldsExhausted = errors.New("workflow run quota hold budget is exhausted")

// IsQuotaHoldsExhausted 供上层判断一次重试被拒是因为额度用完（需要人）而不是
// 节点本来就不能重试。
func IsQuotaHoldsExhausted(err error) bool {
	return errors.Is(err, ErrQuotaHoldsExhausted)
}

// IncrementWorkflowRunHolds 给这一轮 Run 记一次配额挂起，返回记账之后的使用次数
// 与是否记上：超过 cap 时返回 (false, used, nil) 且不写库 —— 额度用完这一下不许
// 把计数继续推高，否则提示行会读成 N/4。used 是**记账后**的计数（成功时是刚占的
// 那一次，拒绝时是当前已用满的值），提示行的「第 N/3 次」直接用它，不必再猜。
// 单条 UPDATE 自增，不读-改-写整行，因此与引擎的 SaveWorkflowRun 互不覆盖。
func (s *TaskStore) IncrementWorkflowRunHolds(ctx context.Context, runID string, cap int) (bool, int, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false, 0, errors.New("workflow run id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE workflow_runs SET holds_used = holds_used + 1 WHERE id=? AND holds_used < ?`, runID, cap)
	if err != nil {
		return false, 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, 0, err
	} else if n == 0 {
		// 行不存在也是 0：区分「额度用完」与「没有这轮 Run」，后者是真的错。
		var used int
		if err := s.db.QueryRowContext(ctx, `SELECT holds_used FROM workflow_runs WHERE id=?`, runID).Scan(&used); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, 0, ErrWorkflowRunNotFound
			}
			return false, 0, err
		}
		return false, used, nil
	}
	var used int
	if err := s.db.QueryRowContext(ctx, `SELECT holds_used FROM workflow_runs WHERE id=?`, runID).Scan(&used); err != nil {
		return true, 0, err
	}
	return true, used, nil
}

// quotaHoldNoticeID 让「同一次挂起」的提示行幂等：节点行 ID 在一次 Run 里唯一
// （run_id,node_id,loop_path,iteration,attempt 唯一），重放会命中同一行。
func quotaHoldNoticeID(nodeRunID string) string {
	return "quota-hold:" + strings.TrimSpace(nodeRunID)
}

// quotaHoldNoticeBody 是给用户看的那一行。时间统一写 UTC：挂起的判定、释放与
// 上游配额窗口都是 UTC，界面里换成本地时间反而对不上「几点恢复」。
func quotaHoldNoticeBody(nodeID, until string, used, cap int) string {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(until))
	stamp := strings.TrimSpace(until)
	if err == nil {
		stamp = at.UTC().Format("2006-01-02 15:04 UTC")
	}
	return fmt.Sprintf("节点 %s 撞到用量上限，已挂起到 %s，到点自动继续（本 Run 第 %d/%d 次）。", nodeID, stamp, used, cap)
}

func quotaHoldsExhaustedBody(cap int) string {
	return fmt.Sprintf("本 Run 的配额挂起额度已用完（%d/%d），节点需要你介入：上游仍在限额，自动等待已停止。", cap, cap)
}

// noteQuotaHold 写一行确定性系统提示（幂等）。与 noteCompletionSystemRow 同一套
// 收口：InsertOrIgnore 保证重放只有一行，broadcast 让界面上立即出现。
func (s *Service) noteQuotaHold(ctx context.Context, store *TaskStore, task Task, id, body, decisionID, severity string) {
	if s == nil || store == nil {
		return
	}
	row := &TaskActivity{
		ID: id, TaskID: task.ID, Type: ActivityTypeAttention,
		Author: "system", Role: "system",
		Status: ActivityStatusCompleted, Severity: severity, Body: body, RouterDecisionID: decisionID,
	}
	created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, row)
	if err != nil || !created {
		return
	}
	s.broadcastWorkflowActivity(ctx, store, row, created, nil)
}
