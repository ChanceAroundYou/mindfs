package kanban

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 看板任务归档（#86）：终态任务可以归档 —— 只是任务自己的一个标记，
// 卡片从工作台隐藏、留在看板「已结束 → 归档」子组。
//
// 级联语义（用户裁定 A）：归档任务会**给相关会话打归档标记**，但绝不删除
// 后代会话或 JSONL。已有的 usecase.ArchiveSession 是破坏性的（先删子树再标记
// 自己），所以任务归档必须走另一条只标记的路径，见 SessionArchiver。
const (
	// TaskArchiveAfter 是「终态任务多久之后自动归档」。
	TaskArchiveAfter = 7 * 24 * time.Hour
	// taskArchiveSweepLimit 限制单次扫描归档的行数：一次扫光几千行会让一个
	// 后台拍点里塞进成千上万次 UPDATE + 会话标记，宁可分多次拍点慢慢来。
	taskArchiveSweepLimit = 200
	// taskArchiveSweepInterval 是低频扫描周期（远高于用户操作频率；归档不是
	// 实时语义，晚一小时没有可见影响）。
	taskArchiveSweepInterval = time.Hour
)

// ErrTaskNotArchivable 表示只有已完成/失败/取消的任务能归档。
// 在跑、等用户、暂停的任务归档会把它从工作台里藏起来，但它的 Run 还在动 ——
// 那正是「看不见却还在跑」的事故形状，所以直接拒绝。
var ErrTaskNotArchivable = errors.New("only finished tasks can be archived")

// SetTaskArchived 只改任务自己的归档标记，不碰 updated_at。
//
// 为什么不动 updated_at：它是看板排序键，也是 7 天自动归档的兜底起算点。
// 归档是「收起来」而不是「这条任务又动了」，改它会让卡片在被归档的瞬间跳到
// 列表最前，也会推后它下次被自动清理的时间。
//
// 幂等：已归档再归档保留首次的归档时间；未归档再取消归档是空操作。
//
// 取消归档会留一个 archive_dismissed_at：自动归档的判据只看时间戳，而时间戳
// 刻意不动，没这个留痕的话下一拍扫描会把用户刚放回来的卡再收走。
func (s *TaskStore) SetTaskArchived(ctx context.Context, taskID string, archived bool) (Task, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if archived {
		if !isTerminalStatus(task.Status) {
			return Task{}, ErrTaskNotArchivable
		}
		if task.ArchivedAt != "" {
			return task, nil
		}
		stamp := s.nowUTC()
		// 显式归档同时清掉「别再自动收我」的留痕：用户重新归档了，语义回到状态本身。
		if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET archived_at = ?, archive_dismissed_at = '' WHERE id = ?`, stamp, task.ID); err != nil {
			return Task{}, err
		}
		task.ArchivedAt = stamp
		// 归档刻意不动 updated_at、也不经过状态机，不留事件的话事后无法追「这张卡
		// 什么时候被收走的」。只在标记真翻转时记，幂等调用不刷屏。
		_ = s.AddEvent(ctx, TaskEvent{
			ID: newID("event"), TaskID: task.ID, Type: "archived",
			Payload: eventPayload(map[string]any{"archived_at": stamp}), CreatedAt: s.nowTime().UTC(),
		})
		return task, nil
	}
	if task.ArchivedAt == "" {
		return task, nil
	}
	dismissed := s.nowUTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET archived_at = '', archive_dismissed_at = ? WHERE id = ?`, dismissed, task.ID); err != nil {
		return Task{}, err
	}
	task.ArchivedAt = ""
	_ = s.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: task.ID, Type: "unarchived",
		Payload: eventPayload(map[string]any{"archive_dismissed_at": dismissed}), CreatedAt: s.nowTime().UTC(),
	})
	return task, nil
}

func (s *TaskStore) nowUTC() string {
	return s.nowTime().UTC().Format(time.RFC3339Nano)
}

// nowTime 是 store 的时钟（测试用它把「7 天前」变成可控输入）。
func (s *TaskStore) nowTime() time.Time {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return now()
}

// ListTasksDueForArchive 返回可以直接自动归档的终态任务。
//
// 起算点按用户的裁定：#7 —— 用 completed_at，空则回退 updated_at。取消/失败
// 的任务 completed_at 本来就是空，回退是常态而不是异常。已经归档的不再返回；
// 用户手动取消过归档的（archive_dismissed_at 非空）也不再返回 —— 那是明确取舍，
// 不能每小时被同一个判断推翻一次。
func (s *TaskStore) ListTasksDueForArchive(ctx context.Context, cutoff time.Time, limit int) ([]Task, error) {
	if limit <= 0 || limit > taskArchiveSweepLimit {
		limit = taskArchiveSweepLimit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskSelectColumns+` FROM tasks
		  WHERE archived_at = ''
		    AND archive_dismissed_at = ''
		    AND status IN (?, ?, ?)
		    AND COALESCE(NULLIF(completed_at, ''), updated_at) < ?
		  ORDER BY updated_at ASC
		  LIMIT ?`,
		StatusSuccess, StatusFail, StatusCancelled,
		cutoff.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, task)
	}
	return items, rows.Err()
}

// TaskRelatedSessionKeys 汇总「属于这个任务」的会话 key：任务主会话、工作流
// 节点会话、活动上绑定的会话，以及 Router 的后台调度会话。
//
// 只做汇总，不判断存在与否 —— 会话可能早就被删了，归档方按 key 逐个尝试、
// 找不到就跳过（见 api 层的 SessionArchiver 适配器）。
func (s *TaskStore) TaskRelatedSessionKeys(ctx context.Context, taskID string) ([]string, error) {
	taskID = strings.TrimSpace(taskID)
	keys := []string{}
	seen := map[string]bool{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	var mainKey string
	if err := s.db.QueryRowContext(ctx, `SELECT main_session_key FROM tasks WHERE id = ?`, taskID).Scan(&mainKey); err != nil {
		return nil, err
	}
	add(mainKey)
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT nr.session_key FROM workflow_node_runs nr
		   JOIN workflow_runs r ON r.id = nr.run_id
		  WHERE r.task_id = ? AND nr.session_key != ''`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		add(key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// session_reuse_policy=run（2026-11-20 起的默认）：一轮 Run 的共享会话只记
	// 在 workflow_runs.session_key 上，节点行反查不到，必须单独收一遍。
	rows, err = s.db.QueryContext(ctx,
		`SELECT DISTINCT session_key FROM workflow_runs WHERE task_id = ? AND session_key != ''`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		add(key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx,
		`SELECT DISTINCT session_key FROM task_activities WHERE task_id = ? AND session_key != ''`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		add(key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Router 会话在 router_sessions 里按 (scope, owner_id) 记录；任务范围是
	// 任务归档时唯一会命中的那一类。表一定存在（migrateRouterTables 建过）。
	rows, err = s.db.QueryContext(ctx,
		`SELECT session_key FROM router_sessions WHERE scope = ? AND owner_id = ? AND session_key != ''`,
		RouterScopeTask, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		add(key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
