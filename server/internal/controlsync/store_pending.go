package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PendingChange 是一条「离线期间本地改过、还没被主节点确认」的记录（spec §4.3）。
//
// BaseHash 是这条记录在主节点上的**祖先值**（边缘最后一次见到的值），三方比对全靠它；
// ChangeID 是这次待推送变更的身份，重推时主节点按它做幂等。
type PendingChange struct {
	ChangeID  string
	Domain    string
	Key       string
	BaseHash  string
	Payload   []byte
	Deleted   bool
	CreatedAt time.Time
}

// NextPendingSeq 领一个本机单调递增的改动序号。
//
// 单条 SQL 里「先读后写」会漏掉并发窗口，这里用显式事务；DSN 带 _txlock=immediate，
// 事务一开就持有写锁，两个并发调用不会拿到同一个号。序号跨重启延续（落 local_seq 表），
// 否则重启后新改动会复用旧 change_id 被主节点当成重复而丢掉。
func (s *Store) NextPendingSeq(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("controlsync: next pending seq: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO local_seq(id, seq) VALUES(1, 0) ON CONFLICT(id) DO NOTHING`); err != nil {
		return 0, fmt.Errorf("controlsync: next pending seq: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE local_seq SET seq = seq + 1 WHERE id = 1`); err != nil {
		return 0, fmt.Errorf("controlsync: next pending seq: %w", err)
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT seq FROM local_seq WHERE id = 1`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("controlsync: next pending seq: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("controlsync: next pending seq: %w", err)
	}
	return seq, nil
}

// PendingChangeID 拼出一个本地改动的身份。origin 是边缘实例 id：两台边缘同时改
// 同一条记录时，序号各自从 1 起，带 origin 才不会撞。
func PendingChangeID(origin, domain, key string, seq int64) string {
	return fmt.Sprintf("%s:%s:%s:%d", origin, domain, key, seq)
}

// PutPending 记下一条待推送变更。
//
// 同一 (domain,key) 离线期间改多次只保留最后一份载荷，BaseHash 保持**第一次**那次
// 的值（祖先值是「我最后在主节点上见到的值」，再改几版也不会变）。ChangeID 却必须
// 换成最后一次的值：主节点按 change_id 去重，若沿用旧 id，「推上去了但回执丢了、
// 本地又改了一版」这版会被当成重复应用而永远上不去 —— 两端静默分叉。
func (s *Store) PutPending(ctx context.Context, p PendingChange) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if err := ValidateRecord(Record{Domain: p.Domain, Key: p.Key}); err != nil {
		return err
	}
	if p.ChangeID == "" {
		return errors.New("controlsync: pending change_id required")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO pending(domain, key, change_id, base_hash, payload, deleted, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(domain, key) DO UPDATE SET
	change_id = excluded.change_id,
	payload = excluded.payload,
	deleted = excluded.deleted`,
		p.Domain, p.Key, p.ChangeID, p.BaseHash, p.Payload, boolInt(p.Deleted),
		s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("controlsync: put pending: %w", err)
	}
	return nil
}

// PendingChange 读一条待推送变更；第二个返回值表示是否存在。
func (s *Store) PendingChange(ctx context.Context, domain, key string) (PendingChange, bool, error) {
	if s == nil || s.db == nil {
		return PendingChange{}, false, errors.New("controlsync: store closed")
	}
	var (
		p         PendingChange
		payload   []byte
		deleted   int
		createdAt string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT change_id, domain, key, base_hash, payload, deleted, created_at
FROM pending WHERE domain = ? AND key = ?`, domain, key).
		Scan(&p.ChangeID, &p.Domain, &p.Key, &p.BaseHash, &payload, &deleted, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingChange{}, false, nil
	}
	if err != nil {
		return PendingChange{}, false, fmt.Errorf("controlsync: get pending: %w", err)
	}
	p.Deleted = deleted != 0
	if len(payload) > 0 {
		p.Payload = payload
	}
	if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = ts
	}
	return p, true, nil
}

// DeletePending 清掉一条待推送变更（已收敛或已被裁决）。
func (s *Store) DeletePending(ctx context.Context, domain, key string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM pending WHERE domain = ? AND key = ?`, domain, key); err != nil {
		return fmt.Errorf("controlsync: delete pending: %w", err)
	}
	return nil
}

// ListPending 按创建顺序取一批待推送变更（推送方每拍只推一页）。
func (s *Store) ListPending(ctx context.Context, limit int) ([]PendingChange, error) {
	return s.listPending(ctx, limit, nil)
}

// ListPendingExcept 取一批待推送变更，但跳过 exclude 里的域（按域冻结，Q4）。
//
// 过滤必须发生在取页之前：冻结域的 pending 会一直留在库里，如果它们排在前面，
// 整页都被它们占满，别的域一条也推不出去 —— 「按域冻结」要的正是「其他域照常」。
func (s *Store) ListPendingExcept(ctx context.Context, limit int, exclude map[string]bool) ([]PendingChange, error) {
	return s.listPending(ctx, limit, exclude)
}

func (s *Store) listPending(ctx context.Context, limit int, exclude map[string]bool) ([]PendingChange, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	if limit <= 0 {
		limit = 200
	}
	query := `
SELECT change_id, domain, key, base_hash, payload, deleted, created_at
FROM pending`
	var args []any
	if domains := sortedTrueKeys(exclude); len(domains) > 0 {
		query += ` WHERE domain NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(domains)), ",") + `)`
		for _, domain := range domains {
			args = append(args, domain)
		}
	}
	query += ` ORDER BY created_at, domain, key LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list pending: %w", err)
	}
	defer rows.Close()
	var out []PendingChange
	for rows.Next() {
		var (
			p         PendingChange
			payload   []byte
			deleted   int
			createdAt string
		)
		if err := rows.Scan(&p.ChangeID, &p.Domain, &p.Key, &p.BaseHash, &payload, &deleted, &createdAt); err != nil {
			return nil, fmt.Errorf("controlsync: scan pending: %w", err)
		}
		p.Deleted = deleted != 0
		if len(payload) > 0 {
			p.Payload = payload
		}
		if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			p.CreatedAt = ts
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: list pending: %w", err)
	}
	return out, nil
}

// sortedTrueKeys 取出 map 里为 true 的键并排序：SQL 的 NOT IN 参数顺序必须稳定，
// 否则同样的冻结集合会生成不同的语句，预编译缓存白建。
func sortedTrueKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key, on := range m {
		if on && key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// DeletePendingIfChange 只在 pending 仍是 changeID 那条时才删。
//
// 推送期间用户又改了一版会让 pending 换上新身份；这时旧回执到了也不能删，
// 否则那次改动永远推不上去。返回是否真的删掉了。
func (s *Store) DeletePendingIfChange(ctx context.Context, domain, key, changeID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("controlsync: store closed")
	}
	if changeID == "" {
		return false, errors.New("controlsync: pending change_id required")
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM pending WHERE domain = ? AND key = ? AND change_id = ?`, domain, key, changeID)
	if err != nil {
		return false, fmt.Errorf("controlsync: delete pending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("controlsync: delete pending: %w", err)
	}
	return n > 0, nil
}

// RebasePendingAfterApplied 在主节点接受了 changeID 之后，把「之后又改的那一版」
// 的共同祖先往前挪到刚被接受的值。
//
// 这里只动 base_hash：载荷已是用户最新意图，身份也已是新的（PutPending 换的），
// 再改任何一个都会把幂等身份和内容搞错位。
func (s *Store) RebasePendingAfterApplied(ctx context.Context, domain, key, appliedHash string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("controlsync: store closed")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE pending SET base_hash = ? WHERE domain = ? AND key = ?`, appliedHash, domain, key)
	if err != nil {
		return false, fmt.Errorf("controlsync: rebase pending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("controlsync: rebase pending: %w", err)
	}
	return n > 0, nil
}

// CountPending 返回待推送变更条数（状态接口与测试用）。
func (s *Store) CountPending(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending`).Scan(&n); err != nil {
		return 0, fmt.Errorf("controlsync: count pending: %w", err)
	}
	return n, nil
}

// RebasePending 把一条待推送变更的共同祖先重定到主节点当前值，载荷换成裁决结果，
// 身份保持不变（`local` 选择：推的还是原样那一版）。
//
// `payload` 选择（用户填了合并值）必须改用 RebasePendingWithID 领一个新身份：
// 载荷变了却沿用旧 change_id，会被主节点的 applied_changes 当成「早已应用过的
// 变更」而静默丢掉。返回是否存在这条 pending —— 期间被别的路径清掉时返回 false，
// 不算错误。
func (s *Store) RebasePending(ctx context.Context, domain, key, baseHash string, payload []byte, deleted bool) (bool, error) {
	return s.RebasePendingWithID(ctx, domain, key, baseHash, "", payload, deleted)
}

// RebasePendingWithID 同 RebasePending，但 changeID 非空时替换幂等身份。
func (s *Store) RebasePendingWithID(ctx context.Context, domain, key, baseHash, changeID string, payload []byte, deleted bool) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("controlsync: store closed")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE pending
SET base_hash = ?,
    change_id = COALESCE(NULLIF(?, ''), change_id),
    payload = ?,
    deleted = ?
WHERE domain = ? AND key = ?`,
		baseHash, changeID, payload, boolInt(deleted), domain, key)
	if err != nil {
		return false, fmt.Errorf("controlsync: rebase pending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("controlsync: rebase pending: %w", err)
	}
	return n > 0, nil
}
