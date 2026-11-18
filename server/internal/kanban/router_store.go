package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Router 台账（Plan 2026-11-14 §17）。三张表在所有节点都存在，但只有
// mode != off 的节点会写入。P0 只写 router_decisions（shadow）；router_sessions
// 从 P1 开始使用，router_task_state 从 P1 开始使用。

const routerTimeLayout = time.RFC3339Nano

const (
	RouterSessionActive = "active"
	RouterSessionIdle   = "idle"
	RouterSessionClosed = "closed"
	RouterSessionFailed = "failed"
)

type RouterSessionRecord struct {
	Scope      RouterScope
	OwnerID    string
	RootID     string
	SessionKey string
	Agent      string
	Model      string
	Effort     string
	ConfigDir  string
	Cwd        string
	PromptHash string
	Status     string
	LastError  string
	LastTurnAt time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// migrateRouterTables 建 Router 三张表。可重复执行；旧库升级后三表为空，
// 行为与改造前完全一致。
func migrateRouterTables(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS router_sessions (
  scope        TEXT NOT NULL,
  owner_id     TEXT NOT NULL,
  root_id      TEXT NOT NULL,
  session_key  TEXT NOT NULL,
  agent        TEXT NOT NULL DEFAULT '',
  model        TEXT NOT NULL DEFAULT '',
  effort       TEXT NOT NULL DEFAULT '',
  config_dir   TEXT NOT NULL DEFAULT '',
  cwd          TEXT NOT NULL DEFAULT '',
  prompt_hash  TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL,
  last_error   TEXT NOT NULL DEFAULT '',
  last_turn_at TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  PRIMARY KEY (scope, owner_id)
);
CREATE TABLE IF NOT EXISTS router_decisions (
  id                     TEXT PRIMARY KEY,
  task_id                TEXT NOT NULL,
  root_id                TEXT NOT NULL,
  scope                  TEXT NOT NULL DEFAULT 'task',
  owner_id               TEXT NOT NULL DEFAULT '',
  user_activity_id       TEXT NOT NULL DEFAULT '',
  placeholder_activity_id TEXT NOT NULL DEFAULT '',
  trigger_kind           TEXT NOT NULL,
  trigger_ref            TEXT NOT NULL DEFAULT '',
  router_session_key     TEXT NOT NULL DEFAULT '',
  turn_id                TEXT NOT NULL,
  brief_hash             TEXT NOT NULL DEFAULT '',
  brief_json             TEXT NOT NULL DEFAULT '{}',
  actions_json           TEXT NOT NULL DEFAULT '[]',
  status                 TEXT NOT NULL,
  applied_seq            INTEGER NOT NULL DEFAULT 0,
  legacy_action_json     TEXT NOT NULL DEFAULT '',
  agreement              TEXT NOT NULL DEFAULT '',
  error                  TEXT NOT NULL DEFAULT '',
  latency_ms             INTEGER NOT NULL DEFAULT 0,
  prompt_tokens          INTEGER NOT NULL DEFAULT 0,
  completion_tokens      INTEGER NOT NULL DEFAULT 0,
  created_at             TEXT NOT NULL,
  updated_at             TEXT NOT NULL,
  UNIQUE(task_id, turn_id)
);
CREATE INDEX IF NOT EXISTS idx_router_decisions_task_created ON router_decisions(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_router_decisions_status ON router_decisions(status, updated_at);
CREATE TABLE IF NOT EXISTS router_task_state (
  task_id            TEXT PRIMARY KEY,
  root_id            TEXT NOT NULL,
  active_kind        TEXT NOT NULL DEFAULT '',
  active_ref_id      TEXT NOT NULL DEFAULT '',
  active_activity_id TEXT NOT NULL DEFAULT '',
  updated_at         TEXT NOT NULL
);
`)
	return err
}

type RouterStore struct {
	db *sql.DB
}

func NewRouterStore(db *sql.DB) *RouterStore {
	return &RouterStore{db: db}
}

// InsertDecision 写入一条决策。同一 (task_id, turn_id) 重复写入返回
// ErrRouterDecisionExists，调用方据此跳过重复触发（幂等闸门）。
var ErrRouterDecisionExists = errors.New("router decision already exists for task turn")

// ErrRouterDecisionNotFound 按 id / turn 找不到决策。
var ErrRouterDecisionNotFound = errors.New("router decision not found")

func (s *RouterStore) InsertDecision(ctx context.Context, decision *RouterDecision) error {
	if decision == nil {
		return errors.New("router decision required")
	}
	decision.ID = strings.TrimSpace(decision.ID)
	decision.TaskID = strings.TrimSpace(decision.TaskID)
	decision.TurnID = strings.TrimSpace(decision.TurnID)
	if decision.ID == "" || decision.TaskID == "" || decision.TurnID == "" {
		return errors.New("router decision id, task id and turn id required")
	}
	now := time.Now().UTC()
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = now
	}
	decision.UpdatedAt = now
	actions, err := json.Marshal(decision.Actions)
	if err != nil {
		return fmt.Errorf("marshal router actions: %w", err)
	}
	legacy := ""
	if decision.LegacyAction != nil {
		raw, err := json.Marshal(decision.LegacyAction)
		if err != nil {
			return fmt.Errorf("marshal legacy action: %w", err)
		}
		legacy = string(raw)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO router_decisions (
  id, task_id, root_id, scope, owner_id, user_activity_id, placeholder_activity_id,
  trigger_kind, trigger_ref, router_session_key, turn_id, brief_hash, brief_json,
  actions_json, status, applied_seq, legacy_action_json, agreement, error,
  latency_ms, prompt_tokens, completion_tokens, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decision.ID, decision.TaskID, decision.RootID, string(decision.Scope), decision.OwnerID,
		decision.UserActivityID, decision.PlaceholderActivityID, string(decision.TriggerKind),
		decision.TriggerRef, decision.RouterSessionKey, decision.TurnID, decision.BriefHash,
		decision.BriefJSON, string(actions), string(decision.Status), decision.AppliedSeq,
		legacy, decision.Agreement, decision.Error, decision.LatencyMS,
		decision.PromptTokens, decision.CompletionTokens,
		decision.CreatedAt.Format(routerTimeLayout), decision.UpdatedAt.Format(routerTimeLayout))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return ErrRouterDecisionExists
		}
		return err
	}
	return nil
}

func (s *RouterStore) DecisionByTurn(ctx context.Context, taskID, turnID string) (*RouterDecision, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+routerDecisionColumns+` FROM router_decisions WHERE task_id = ? AND turn_id = ? LIMIT 1`,
		strings.TrimSpace(taskID), strings.TrimSpace(turnID))
	return scanRouterDecision(row)
}

func (s *RouterStore) GetDecision(ctx context.Context, id string) (*RouterDecision, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+routerDecisionColumns+` FROM router_decisions WHERE id = ? LIMIT 1`, strings.TrimSpace(id))
	return scanRouterDecision(row)
}

// ListDecisions 按时间倒序返回决策。before 非零时只返回更早的记录；
// limit 默认 50，上限 200。
func (s *RouterStore) ListDecisions(ctx context.Context, taskID string, limit int, before time.Time) ([]*RouterDecision, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("task id required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT ` + routerDecisionColumns + ` FROM router_decisions WHERE task_id = ?`
	args := []any{taskID}
	if !before.IsZero() {
		query += ` AND created_at < ?`
		args = append(args, before.UTC().Format(routerTimeLayout))
	}
	query += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*RouterDecision, 0, limit)
	for rows.Next() {
		decision, err := scanRouterDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, rows.Err()
}

// LatestProblemDecision 返回 root 下最近一条带错误的决策，供诊断面板显示。
func (s *RouterStore) LatestProblemDecision(ctx context.Context, rootID string) (*RouterDecision, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+routerDecisionColumns+`
FROM router_decisions
WHERE root_id = ? AND error <> ''
ORDER BY created_at DESC, rowid DESC LIMIT 1`, strings.TrimSpace(rootID))
	decision, err := scanRouterDecision(row)
	if errors.Is(err, ErrRouterDecisionNotFound) {
		return nil, nil
	}
	return decision, err
}

// RecoverApplyingDecisions 把进程崩溃时停在 applying 的决策标记为 failed。
// P0 没有 applying；P1 的 apply 恢复用同一条路径避免「永远 applying」。
func (s *RouterStore) RecoverApplyingDecisions(ctx context.Context, reason string) (int64, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "router apply interrupted"
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE router_decisions
SET status = ?, error = ?, updated_at = ?
WHERE status = ?`,
		string(RouterDecisionFailed), reason, time.Now().UTC().Format(routerTimeLayout), string(RouterDecisionApplying))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClaimAppliedSeq 是 route 模式的动作幂等闸门：只有 applied_seq 精确等于本次
// 动作的 seq 时才把它推进到 seq+1。返回 false 表示这一条已经应用过（或决策
// 已不在 applying），调用方必须跳过，不能重复建活动/开 Run。
func (s *RouterStore) ClaimAppliedSeq(ctx context.Context, id string, seq int) (bool, error) {
	if seq < 0 {
		return false, errors.New("router action seq must be >= 0")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE router_decisions
SET applied_seq = ?, updated_at = ?
WHERE id = ? AND applied_seq = ? AND status = ?`,
		seq+1, time.Now().UTC().Format(routerTimeLayout), strings.TrimSpace(id), seq, string(RouterDecisionApplying))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkDecisionStatus 只改状态与错误文案，不重写动作/会话。route 模式应用完成
// 或失败时用它，避免把已经落库的 actions_json 再序列化一遍。
func (s *RouterStore) MarkDecisionStatus(ctx context.Context, id string, status RouterDecisionStatus, errText string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE router_decisions SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		string(status), errText, time.Now().UTC().Format(routerTimeLayout), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRouterDecisionNotFound
	}
	return nil
}

func (s *RouterStore) UpdateDecisionResult(ctx context.Context, id string, status RouterDecisionStatus, actions []RouterAction, routerSessionKey, agreement, errText string, latencyMS, promptTokens, completionTokens int) error {
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("marshal router actions: %w", err)
	}
	if actions == nil {
		actionsJSON = []byte("[]")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE router_decisions
SET status = ?, actions_json = ?, router_session_key = ?, agreement = ?, error = ?, latency_ms = ?,
    prompt_tokens = ?, completion_tokens = ?, updated_at = ?
WHERE id = ?`,
		string(status), string(actionsJSON), strings.TrimSpace(routerSessionKey), agreement, errText, latencyMS, promptTokens, completionTokens,
		time.Now().UTC().Format(routerTimeLayout), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("router decision not found")
	}
	return nil
}

// DeleteTaskRouterData 删除一个任务的全部 Router 运行数据。tasks/runs/activities
// 本身由 TaskStore.DeleteTask 删除，这里只清 Router 独有的表。
func (s *RouterStore) DeleteTaskRouterData(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM router_decisions WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM router_task_state WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM router_sessions WHERE scope = ? AND owner_id = ?`, string(RouterScopeTask), taskID); err != nil {
		return err
	}
	// 事件账本的 unique_key 含 task_id：不清掉的话，任务重建同 ID 会命中旧键，
	// 新 Run 的事件永远插不进去。
	_, err := s.db.ExecContext(ctx, `DELETE FROM router_events WHERE task_id = ?`, taskID)
	return err
}

func (s *RouterStore) UpsertSession(ctx context.Context, record *RouterSessionRecord) error {
	if record == nil {
		return errors.New("router session record required")
	}
	record.OwnerID = strings.TrimSpace(record.OwnerID)
	record.RootID = strings.TrimSpace(record.RootID)
	record.SessionKey = strings.TrimSpace(record.SessionKey)
	if record.OwnerID == "" || record.SessionKey == "" {
		return errors.New("router session owner id and session key required")
	}
	scope := record.Scope
	if scope == "" {
		scope = RouterScopeTask
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO router_sessions (
  scope, owner_id, root_id, session_key, agent, model, effort, config_dir, cwd,
  prompt_hash, status, last_error, last_turn_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, owner_id) DO UPDATE SET
  root_id = excluded.root_id,
  session_key = excluded.session_key,
  agent = excluded.agent,
  model = excluded.model,
  effort = excluded.effort,
  config_dir = excluded.config_dir,
  cwd = excluded.cwd,
  prompt_hash = excluded.prompt_hash,
  status = excluded.status,
  last_error = excluded.last_error,
  last_turn_at = excluded.last_turn_at,
  updated_at = excluded.updated_at`,
		string(scope), record.OwnerID, record.RootID, record.SessionKey, record.Agent,
		record.Model, record.Effort, record.ConfigDir, record.Cwd, record.PromptHash,
		record.Status, record.LastError, formatRouterTime(record.LastTurnAt),
		record.CreatedAt.Format(routerTimeLayout), record.UpdatedAt.Format(routerTimeLayout))
	return err
}

func (s *RouterStore) Session(ctx context.Context, scope RouterScope, ownerID string) (*RouterSessionRecord, error) {
	if scope == "" {
		scope = RouterScopeTask
	}
	row := s.db.QueryRowContext(ctx, `
SELECT scope, owner_id, root_id, session_key, agent, model, effort, config_dir, cwd,
       prompt_hash, status, last_error, last_turn_at, created_at, updated_at
FROM router_sessions WHERE scope = ? AND owner_id = ? LIMIT 1`,
		string(scope), strings.TrimSpace(ownerID))
	var (
		record                     RouterSessionRecord
		scopeText                  string
		lastTurn, created, updated string
	)
	if err := row.Scan(&scopeText, &record.OwnerID, &record.RootID, &record.SessionKey,
		&record.Agent, &record.Model, &record.Effort, &record.ConfigDir, &record.Cwd,
		&record.PromptHash, &record.Status, &record.LastError, &lastTurn, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	record.Scope = RouterScope(scopeText)
	record.LastTurnAt = parseRouterTime(lastTurn)
	record.CreatedAt = parseRouterTime(created)
	record.UpdatedAt = parseRouterTime(updated)
	return &record, nil
}

func (s *RouterStore) DeleteSession(ctx context.Context, scope RouterScope, ownerID string) error {
	if scope == "" {
		scope = RouterScopeTask
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM router_sessions WHERE scope = ? AND owner_id = ?`, string(scope), strings.TrimSpace(ownerID))
	return err
}

// ListSessions 按 root 返回某一作用域下的 Router 会话，供空闲清扫使用。
func (s *RouterStore) ListSessions(ctx context.Context, scope RouterScope, rootID string) ([]*RouterSessionRecord, error) {
	if scope == "" {
		scope = RouterScopeTask
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT scope, owner_id, root_id, session_key, agent, model, effort, config_dir, cwd,
       prompt_hash, status, last_error, last_turn_at, created_at, updated_at
FROM router_sessions WHERE scope = ? AND root_id = ?`,
		string(scope), strings.TrimSpace(rootID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*RouterSessionRecord, 0)
	for rows.Next() {
		var (
			record                     RouterSessionRecord
			scopeText                  string
			lastTurn, created, updated string
		)
		if err := rows.Scan(&scopeText, &record.OwnerID, &record.RootID, &record.SessionKey,
			&record.Agent, &record.Model, &record.Effort, &record.ConfigDir, &record.Cwd,
			&record.PromptHash, &record.Status, &record.LastError, &lastTurn, &created, &updated); err != nil {
			return nil, err
		}
		record.Scope = RouterScope(scopeText)
		record.LastTurnAt = parseRouterTime(lastTurn)
		record.CreatedAt = parseRouterTime(created)
		record.UpdatedAt = parseRouterTime(updated)
		out = append(out, &record)
	}
	return out, rows.Err()
}

// UpdateSessionStatus 更新 Router 会话状态（空闲关闭/失败）。
func (s *RouterStore) UpdateSessionStatus(ctx context.Context, scope RouterScope, ownerID, status, errText string) error {
	if scope == "" {
		scope = RouterScopeTask
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE router_sessions SET status = ?, last_error = ?, updated_at = ?
WHERE scope = ? AND owner_id = ?`,
		status, errText, time.Now().UTC().Format(routerTimeLayout), string(scope), strings.TrimSpace(ownerID))
	return err
}

const routerDecisionColumns = `id, task_id, root_id, scope, owner_id, user_activity_id,
placeholder_activity_id, trigger_kind, trigger_ref, router_session_key, turn_id,
brief_hash, brief_json, actions_json, status, applied_seq, legacy_action_json,
agreement, error, latency_ms, prompt_tokens, completion_tokens, created_at, updated_at`

func scanRouterDecision(row scanner) (*RouterDecision, error) {
	var (
		decision               RouterDecision
		scope, triggerKind     string
		actionsRaw, legacyRaw  string
		createdRaw, updatedRaw string
	)
	if err := row.Scan(
		&decision.ID, &decision.TaskID, &decision.RootID, &scope, &decision.OwnerID,
		&decision.UserActivityID, &decision.PlaceholderActivityID, &triggerKind,
		&decision.TriggerRef, &decision.RouterSessionKey, &decision.TurnID,
		&decision.BriefHash, &decision.BriefJSON, &actionsRaw, &decision.Status,
		&decision.AppliedSeq, &legacyRaw, &decision.Agreement, &decision.Error,
		&decision.LatencyMS, &decision.PromptTokens, &decision.CompletionTokens,
		&createdRaw, &updatedRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRouterDecisionNotFound
		}
		return nil, err
	}
	decision.Scope = RouterScope(scope)
	decision.TriggerKind = RouterTriggerKind(triggerKind)
	if strings.TrimSpace(actionsRaw) != "" {
		if err := json.Unmarshal([]byte(actionsRaw), &decision.Actions); err != nil {
			return nil, fmt.Errorf("decode router actions: %w", err)
		}
	}
	if strings.TrimSpace(legacyRaw) != "" {
		var legacy RouterLegacyAction
		if err := json.Unmarshal([]byte(legacyRaw), &legacy); err == nil {
			decision.LegacyAction = &legacy
		}
	}
	decision.CreatedAt = parseRouterTime(createdRaw)
	decision.UpdatedAt = parseRouterTime(updatedRaw)
	return &decision, nil
}

func parseRouterTime(raw string) time.Time {
	parsed, err := time.Parse(routerTimeLayout, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func formatRouterTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(routerTimeLayout)
}
