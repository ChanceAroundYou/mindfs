package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Router 事件账本的落库实现（Plan 2026-11-18，R1 T5）。
//
// 与 Router 决策台账（router_store.go）共用同一个 *sql.DB。表由
// migrateRouterEventTable 建：TaskStore 的启动迁移不在本任务允许改动的文件里，
// 所以这里在第一次使用时惰性建表（CREATE TABLE IF NOT EXISTS，可重复执行）。
//
// 生命周期：事件以 pending 落库 → ClaimEvent 做 CAS 领取 → 调用方执行副作用 →
// 副作用成功后才 CompleteEvent(done)。直接对 pending 行 complete 会报错，
// 避免「先推进游标后丢副作用」。

// ErrRouterEventNotFound 表示按 id 找不到事件行。
var ErrRouterEventNotFound = errors.New("router event not found")

const routerEventColumns = `id, root_id, task_id, run_id, kind, instance_key, unique_key,
user_activity_id, turn_id, chain_depth, source_decision_id, attempt, payload_json, status,
claimed_by, claimed_at, completed_at, last_error, created_at, updated_at`

// migrateRouterEventTable 建事件账本表。可重复执行；旧库升级后表为空。
func migrateRouterEventTable(db *sql.DB) error {
	if db == nil {
		return errors.New("router event migration requires a database")
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS router_events (
  id                 TEXT PRIMARY KEY,
  root_id            TEXT NOT NULL,
  task_id            TEXT NOT NULL,
  run_id             TEXT NOT NULL DEFAULT '',
  kind               TEXT NOT NULL,
  instance_key       TEXT NOT NULL,
  unique_key         TEXT NOT NULL,
  user_activity_id   TEXT NOT NULL DEFAULT '',
  turn_id            TEXT NOT NULL DEFAULT '',
  chain_depth        INTEGER NOT NULL DEFAULT 0,
  source_decision_id TEXT NOT NULL DEFAULT '',
  attempt            INTEGER NOT NULL DEFAULT 0,
  payload_json       TEXT NOT NULL DEFAULT '{}',
  status             TEXT NOT NULL,
  claimed_by         TEXT NOT NULL DEFAULT '',
  claimed_at         TEXT NOT NULL DEFAULT '',
  completed_at       TEXT NOT NULL DEFAULT '',
  last_error         TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL,
  UNIQUE(unique_key)
);
CREATE INDEX IF NOT EXISTS idx_router_events_pending ON router_events(status, created_at, id);
CREATE INDEX IF NOT EXISTS idx_router_events_task ON router_events(task_id, created_at);
`)
	return err
}

var (
	routerEventMigrateMu sync.Mutex
	routerEventMigrated  = map[*sql.DB]struct{}{}
)

// ensureRouterEventTable 每个连接只跑一次迁移；重开的库（新 *sql.DB）会再跑，
// CREATE TABLE IF NOT EXISTS 保证不报错也不改历史行。
func (s *RouterStore) ensureRouterEventTable() error {
	if s == nil || s.db == nil {
		return errors.New("router store requires a database")
	}
	routerEventMigrateMu.Lock()
	_, done := routerEventMigrated[s.db]
	routerEventMigrateMu.Unlock()
	if done {
		return nil
	}
	if err := migrateRouterEventTable(s.db); err != nil {
		return fmt.Errorf("migrate router events: %w", err)
	}
	routerEventMigrateMu.Lock()
	routerEventMigrated[s.db] = struct{}{}
	routerEventMigrateMu.Unlock()
	return nil
}

// InsertEvent 写入一条事件。同一 UniqueKey 重复写入返回 (已有行, false, nil)：
// 不报错、不改已有行，调用方据此跳过重复事件。
func (s *RouterStore) InsertEvent(ctx context.Context, ev RouterEvent) (RouterEvent, bool, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return RouterEvent{}, false, err
	}
	ev.ID = strings.TrimSpace(ev.ID)
	if ev.ID == "" {
		ev.ID = newID("revt")
	}
	ev.RootID = strings.TrimSpace(ev.RootID)
	ev.TaskID = strings.TrimSpace(ev.TaskID)
	ev.RunID = strings.TrimSpace(ev.RunID)
	ev.InstanceKey = strings.TrimSpace(ev.InstanceKey)
	ev.TurnID = strings.TrimSpace(ev.TurnID)
	ev.UserActivityID = strings.TrimSpace(ev.UserActivityID)
	ev.SourceDecisionID = strings.TrimSpace(ev.SourceDecisionID)
	if ev.RootID == "" || ev.TaskID == "" {
		return RouterEvent{}, false, errors.New("router event requires root id and task id")
	}
	if !knownRouterEventKind(ev.Kind) {
		return RouterEvent{}, false, fmt.Errorf("unknown router event kind %q", ev.Kind)
	}
	if ev.RunID == "" {
		return RouterEvent{}, false, fmt.Errorf("router event %s requires a run id", ev.Kind)
	}
	if ev.InstanceKey == "" {
		return RouterEvent{}, false, fmt.Errorf("router event %s requires an instance key", ev.Kind)
	}
	if ev.ChainDepth < 0 {
		return RouterEvent{}, false, fmt.Errorf("router event chain depth %d must be >= 0", ev.ChainDepth)
	}
	if ev.Attempt < 0 {
		return RouterEvent{}, false, fmt.Errorf("router event attempt %d must be >= 0", ev.Attempt)
	}
	if len(ev.PayloadJSON) > RouterEventMaxPayloadBytes {
		return RouterEvent{}, false, fmt.Errorf("router event payload %d bytes exceeds %d byte limit",
			len(ev.PayloadJSON), RouterEventMaxPayloadBytes)
	}
	if ev.PayloadJSON == "" {
		ev.PayloadJSON = "{}"
	}
	ev.Status = strings.TrimSpace(ev.Status)
	if ev.Status == "" {
		ev.Status = RouterEventPending
	}
	if !knownRouterEventStatus(ev.Status) {
		return RouterEvent{}, false, fmt.Errorf("unknown router event status %q", ev.Status)
	}
	now := time.Now().UTC()
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = now
	} else {
		ev.CreatedAt = ev.CreatedAt.UTC()
	}
	ev.UpdatedAt = ev.CreatedAt

	uniqueKey := RouterEventUniqueKey(ev.TaskID, ev.RunID, ev.Kind, ev.InstanceKey)
	if ev.UniqueKey != "" && ev.UniqueKey != uniqueKey {
		return RouterEvent{}, false, fmt.Errorf("router event unique key %q does not match computed %q", ev.UniqueKey, uniqueKey)
	}
	ev.UniqueKey = uniqueKey

	res, err := s.db.ExecContext(ctx, `
INSERT INTO router_events (`+routerEventColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(unique_key) DO NOTHING`,
		ev.ID, ev.RootID, ev.TaskID, ev.RunID, string(ev.Kind), ev.InstanceKey, ev.UniqueKey,
		ev.UserActivityID, ev.TurnID, ev.ChainDepth, ev.SourceDecisionID, ev.Attempt,
		ev.PayloadJSON, ev.Status, ev.ClaimedBy, formatRouterTime(ev.ClaimedAt),
		formatRouterTime(ev.CompletedAt), ev.LastError,
		ev.CreatedAt.Format(routerTimeLayout), ev.UpdatedAt.Format(routerTimeLayout))
	if err != nil {
		return RouterEvent{}, false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return RouterEvent{}, false, err
	}
	if inserted == 0 {
		existing, err := s.eventByUniqueKey(ctx, uniqueKey)
		if err != nil {
			return RouterEvent{}, false, err
		}
		return existing, false, nil
	}
	return ev, true, nil
}

// ClaimEvent 是 CAS 领取：只有 status='pending' 的行能变成 claimed。
// 返回 false 表示已被他人领取或已终态；行不存在返回 ErrRouterEventNotFound。
func (s *RouterStore) ClaimEvent(ctx context.Context, eventID, claimerID string) (bool, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return false, err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false, errors.New("router event id required")
	}
	now := time.Now().UTC().Format(routerTimeLayout)
	res, err := s.db.ExecContext(ctx, `
UPDATE router_events SET status = ?, claimed_by = ?, claimed_at = ?, updated_at = ?
WHERE id = ? AND status = ?`,
		RouterEventClaimed, strings.TrimSpace(claimerID), now, now, eventID, RouterEventPending)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if _, err := s.eventStatus(ctx, eventID); err != nil {
		return false, err
	}
	return false, nil
}

// CompleteEvent 把 claimed 行推进到终态。只有 done|failed|skipped 是合法目标；
// done 必须由调用方在副作用成功之后调用（先推进游标后丢副作用会变成 failed）。
func (s *RouterStore) CompleteEvent(ctx context.Context, eventID, status, errText string) error {
	if err := s.ensureRouterEventTable(); err != nil {
		return err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return errors.New("router event id required")
	}
	status = strings.TrimSpace(status)
	switch status {
	case RouterEventDone, RouterEventFailed, RouterEventSkipped:
	default:
		return fmt.Errorf("router event cannot complete to status %q (want done|failed|skipped)", status)
	}
	now := time.Now().UTC().Format(routerTimeLayout)
	res, err := s.db.ExecContext(ctx, `
UPDATE router_events SET status = ?, last_error = ?, completed_at = ?, updated_at = ?
WHERE id = ? AND status = ?`,
		status, errText, now, now, eventID, RouterEventClaimed)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	current, err := s.eventStatus(ctx, eventID)
	if err != nil {
		return err
	}
	if current == RouterEventPending {
		return fmt.Errorf("router event %s must be claimed before completing (status %q)", eventID, current)
	}
	return fmt.Errorf("router event %s already %s", eventID, current)
}

// ListPendingEvents 按 created_at, id 稳定返回待处理事件。
func (s *RouterStore) ListPendingEvents(ctx context.Context, limit int) ([]RouterEvent, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+routerEventColumns+`
FROM router_events WHERE status = ? ORDER BY created_at ASC, id ASC LIMIT ?`,
		RouterEventPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRouterEvents(rows)
}

// ListEventsByTask 返回一个任务的全部事件（按写入时间正序），供诊断与测试。
func (s *RouterStore) ListEventsByTask(ctx context.Context, taskID string) ([]RouterEvent, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return nil, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("task id required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+routerEventColumns+`
FROM router_events WHERE task_id = ? ORDER BY created_at ASC, id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRouterEvents(rows)
}

// RecoverClaimedEvents 是启动对账：上次进程遗留的 claimed 行恢复为 pending，
// LastError 记录原因（保留原领取者）。done/failed/skipped 绝不复活。
func (s *RouterStore) RecoverClaimedEvents(ctx context.Context, reason string) (int, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "router event claim interrupted"
	}
	now := time.Now().UTC().Format(routerTimeLayout)
	res, err := s.db.ExecContext(ctx, `
UPDATE router_events
SET status = ?,
    last_error = CASE WHEN claimed_by <> '' THEN ? || ' (claimed by ' || claimed_by || ')' ELSE ? END,
    claimed_by = '',
    claimed_at = '',
    updated_at = ?
WHERE status = ?`,
		RouterEventPending, reason, reason, now, RouterEventClaimed)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ReclaimStaleEvents 是领取租约的兜底：claimed 行的 claimed_at 早于 before 就
// 打回 pending，LastError 记录原因与原领取者。与 RecoverClaimedEvents 的区别是
// 时间条件 —— 那个只在启动跑一次（进程还活着就永远等不到），这个可以每拍跑。
//
// 判据是 claimed_at 不是 created_at：排队很久但刚被领走的事件是活的。
// done/failed/skipped 绝不复活。
func (s *RouterStore) ReclaimStaleEvents(ctx context.Context, before time.Time, reason string) (int, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "router event lease expired"
	}
	now := time.Now().UTC().Format(routerTimeLayout)
	res, err := s.db.ExecContext(ctx, `
UPDATE router_events
SET status = ?,
    last_error = CASE WHEN claimed_by <> '' THEN ? || ' (claimed by ' || claimed_by || ')' ELSE ? END,
    claimed_by = '',
    claimed_at = '',
    updated_at = ?
WHERE status = ? AND claimed_at <> '' AND claimed_at < ?`,
		RouterEventPending, reason, reason, now, RouterEventClaimed, before.UTC().Format(routerTimeLayout))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// PruneEvents 回收已终态且早于 cutoff 的行，单次最多 limit 行。
//
// 账本是 append-only 的，但「append-only」说的是不覆写，不是永不回收：每个 Run
// 结算都落一行，节点跑上一年这张表就无限长，而没有任何东西读它（诊断只看最近的）。
//
// 只删 done|failed|skipped。pending/claimed 是还没办完的事，再老也得留着 ——
// 删掉它们等于静默丢事件。分批（子查询取 id + LIMIT）是为了不长事务扫表：
// 一次删不完下一拍接着删，不依赖 SQLITE_ENABLE_UPDATE_DELETE_LIMIT 编译选项。
func (s *RouterStore) PruneEvents(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if err := s.ensureRouterEventTable(); err != nil {
		return 0, err
	}
	if limit <= 0 {
		limit = routerEventPruneLimit
	}
	// 与 scanRouterEventSourcesBounded 同一口径：created_at 是 RFC3339Nano，
	// 小数位长度不定，直接字符串比较会把 '...:00.5Z' 排到 '...:00Z' 前面。
	// 截到整秒再比，代价是保留边界有 1 秒粒度，对按天计的保留期无所谓。
	cut := cutoff.UTC().Format(routerCoordinateSQLTimeLayout)
	res, err := s.db.ExecContext(ctx, `
DELETE FROM router_events
WHERE id IN (
  SELECT id FROM router_events
  WHERE status IN (?, ?, ?) AND substr(created_at, 1, 19) < ?
  ORDER BY created_at ASC, id ASC
  LIMIT ?
)`, RouterEventDone, RouterEventFailed, RouterEventSkipped, cut, limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *RouterStore) eventByUniqueKey(ctx context.Context, uniqueKey string) (RouterEvent, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+routerEventColumns+` FROM router_events WHERE unique_key = ? LIMIT 1`, uniqueKey)
	return scanRouterEvent(row)
}

func (s *RouterStore) eventStatus(ctx context.Context, eventID string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM router_events WHERE id = ? LIMIT 1`, eventID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRouterEventNotFound
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

func scanRouterEvents(rows *sql.Rows) ([]RouterEvent, error) {
	events := make([]RouterEvent, 0)
	for rows.Next() {
		ev, err := scanRouterEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func scanRouterEvent(row scanner) (RouterEvent, error) {
	var (
		ev                     RouterEvent
		kind                   string
		claimedAt, completedAt string
		createdRaw, updatedRaw string
	)
	if err := row.Scan(&ev.ID, &ev.RootID, &ev.TaskID, &ev.RunID, &kind, &ev.InstanceKey,
		&ev.UniqueKey, &ev.UserActivityID, &ev.TurnID, &ev.ChainDepth, &ev.SourceDecisionID,
		&ev.Attempt, &ev.PayloadJSON, &ev.Status, &ev.ClaimedBy, &claimedAt, &completedAt,
		&ev.LastError, &createdRaw, &updatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RouterEvent{}, ErrRouterEventNotFound
		}
		return RouterEvent{}, err
	}
	ev.Kind = RouterEventKind(kind)
	ev.ClaimedAt = parseRouterTime(claimedAt)
	ev.CompletedAt = parseRouterTime(completedAt)
	ev.CreatedAt = parseRouterTime(createdRaw)
	ev.UpdatedAt = parseRouterTime(updatedRaw)
	return ev, nil
}
