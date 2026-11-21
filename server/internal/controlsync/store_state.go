package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// State 是 state 表一行的只读视图（边缘用于对外状态，主节点也用同一份表）。
type State struct {
	InstanceID    string
	Mode          string
	BaseRev       int64
	LastPrimaryAt time.Time
	LastError     string
}

// LoadState 读单行状态；表里还没有行时返回零值（mode 空串 = 尚未初始化）。
func (s *Store) LoadState(ctx context.Context) (State, error) {
	if s == nil || s.db == nil {
		return State{}, errors.New("controlsync: store closed")
	}
	var (
		st        State
		lastAt    string
		lastError string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT instance_id, mode, base_rev, last_primary_at, last_error FROM state WHERE id = 1`).
		Scan(&st.InstanceID, &st.Mode, &st.BaseRev, &lastAt, &lastError)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("controlsync: load state: %w", err)
	}
	st.LastError = lastError
	if lastAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, lastAt); err == nil {
			st.LastPrimaryAt = ts
		}
	}
	return st, nil
}

// SetMode 持久化模式；其余列保持不动（base_rev 由 ApplyRemoteChanges 单独推进）。
func (s *Store) SetMode(ctx context.Context, mode Mode) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if !validMode(mode) {
		return fmt.Errorf("controlsync: invalid mode %q", mode)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, mode) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET mode = excluded.mode`, string(mode)); err != nil {
		return fmt.Errorf("controlsync: save mode: %w", err)
	}
	return nil
}

// RecordProbe 记一次探测结果：成功更新 last_primary_at 并清错误，失败只写 last_error。
//
// 失败时刻意不动 last_primary_at —— 「上次成功连上主节点是什么时候」正是断线排查
// 最需要的数字，被失败覆盖掉就没了。
func (s *Store) RecordProbe(ctx context.Context, at time.Time, errText string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if errText == "" {
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, last_primary_at, last_error) VALUES(1, ?, '')
ON CONFLICT(id) DO UPDATE SET last_primary_at = excluded.last_primary_at, last_error = ''`,
			at.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("controlsync: record probe: %w", err)
		}
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, last_error) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET last_error = excluded.last_error`, errText); err != nil {
		return fmt.Errorf("controlsync: record probe: %w", err)
	}
	return nil
}

// SetInstanceID 只在首次启动时写一次：它是这台边缘的身份，重启不得更换。
func (s *Store) SetInstanceID(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, instance_id) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET instance_id = excluded.instance_id`, id); err != nil {
		return fmt.Errorf("controlsync: save instance id: %w", err)
	}
	return nil
}
