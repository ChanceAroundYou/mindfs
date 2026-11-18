package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Router 会话行（router_sessions）。从 router_store.go 纯移动出来：账本文件
// 已接近 500 行硬线，而这里只负责一个会话记录的生命周期。
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
