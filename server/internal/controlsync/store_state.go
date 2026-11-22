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
	InstanceID string
	// PrimaryInstanceID 是边缘记下的主节点身份：主节点被重建/换机后它会变，
	// 追平前比对不一致就拒绝合并（见 Replicator.verifyOrigin）。
	PrimaryInstanceID string
	// BlockedReason 非空表示追平被锁住（主节点身份或 schema 不符）。它必须持久化：
	// 重启后自动重新追平只会再撞上同一个不符，反复失败。
	BlockedReason string
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
		primaryID string
		blocked   string
		lastAt    string
		lastError string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT instance_id, primary_instance_id, blocked_reason, mode, base_rev, last_primary_at, last_error FROM state WHERE id = 1`).
		Scan(&st.InstanceID, &primaryID, &blocked, &st.Mode, &st.BaseRev, &lastAt, &lastError)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("controlsync: load state: %w", err)
	}
	st.PrimaryInstanceID = primaryID
	st.BlockedReason = blocked
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

// SetPrimaryInstanceID 记下对面主节点的身份。只在边缘第一次成功取到 head 时写；
// 之后它不随主节点变化自动更新 —— 变了正是要停下来让人处理的信号。
func (s *Store) SetPrimaryInstanceID(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, primary_instance_id) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET primary_instance_id = excluded.primary_instance_id`, id); err != nil {
		return fmt.Errorf("controlsync: save primary instance id: %w", err)
	}
	return nil
}

// SetBlockedReason 记录/清除追平锁。空串即解锁。
func (s *Store) SetBlockedReason(ctx context.Context, reason string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO state(id, blocked_reason) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET blocked_reason = excluded.blocked_reason`, reason); err != nil {
		return fmt.Errorf("controlsync: save blocked reason: %w", err)
	}
	return nil
}

// ResetReplicaForAdopt 是「接受当前这台主节点」的落地动作：副本是按旧主节点的
// rev 序列与内容建的，对新主节点没有意义，所以整份丢掉重来。
//
// 丢掉的是副本（records / base_rev / 冲突卡）；**保留 pending** —— 那是用户在离线
// 期间真的改过的东西，必须留着交给恢复后重新比对（大概率会变成冲突卡，但那是有
// 记录的、可裁决的，不是静默丢弃）。记下的主节点身份一并清空，下一次取 head 时
// 重新采纳。
func (s *Store) ResetReplicaForAdopt(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("controlsync: begin adopt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`DELETE FROM records`,
		`DELETE FROM conflicts`,
		`UPDATE state SET primary_instance_id = '', blocked_reason = '', base_rev = 0 WHERE id = 1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("controlsync: adopt reset: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("controlsync: adopt commit: %w", err)
	}
	return nil
}
