package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// spec §4.2 的保留策略：变更流与墓碑按时间与行数双重设限。
const (
	// ChangeRetention 是变更流（含墓碑）的保留期。边缘断线超过这个时长就该
	// 重新采纳主节点，而不是指望主节点无限留着历史。
	ChangeRetention = 90 * 24 * time.Hour
	// ChangeMaxRows 是变更流的行数上限。按条数兜底，防止高频改动把库撑爆。
	ChangeMaxRows = 500000
	// PruneBatchSize 是单次清理最多删除的行数。一次删几十万行会把写锁攥在手里，
	// 期间采集与边缘拉取全部停摆；超出的部分留给下一拍。
	PruneBatchSize = 5000
)

// ErrRetentionGap：请求的起点已经落在被清理掉的区间里。
//
// 它不是暂时故障：重试再多次，那段变更也回不来了。调用方（边缘）必须据此停下、
// 留在本地模式，由人决定是否采纳主节点重来 —— 静默跳过等于让副本永久缺一段控制面。
var ErrRetentionGap = errors.New("controlsync: change log retention gap")

// PruneOptions 是清理参数。零值取默认（spec §4.2 的 90 天 / 50 万行 / 单批 5000），
// 测试用它把时间与批量压小。
type PruneOptions struct {
	Now       time.Time
	Retention time.Duration
	MaxRows   int
	Batch     int
}

// PrunedThroughRev 返回已被清理到哪一条 rev（含）。0 表示没有清理过。
//
// 这个水位同时是 rev 分配的下限、HeadRev 的下限与「保留窗口缺口」的判据：
// 三者必须同源，否则删空之后新 rev 会从 1 重新发号，而边缘的水位停在旧值 ——
// 新变更会被 `rev <= base_rev` 的幂等守卫全部跳过，且水位那一侧毫无异常。
func (s *Store) PrunedThroughRev(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	return prunedThroughRev(ctx, s.db)
}

// revQuerier 让水位读取能同时跑在连接池与事务上。
type revQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func prunedThroughRev(ctx context.Context, q revQuerier) (int64, error) {
	var rev int64
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT pruned_through_rev FROM state WHERE id = 1), 0)`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("controlsync: read pruned_through_rev: %w", err)
	}
	return rev, nil
}

// PruneChanges 清理变更流：先删保留期之外的，再在超出 MaxRows 时从最旧的删起。
//
// 只删变更流。records 是当前快照（边缘的副本还要靠它交接），applied_changes 是
// 幂等凭据（行小、且删掉会把「已应用过」的推送重新判成冲突），两者都不在这里回收。
//
// 一次调用最多删 Batch 行；返回实际删除条数。整个过程一个事务，水位推进与删除
// 同生共死：删了却没推进下限，下一批新 rev 就可能落回已删区间。
func (s *Store) PruneChanges(ctx context.Context, opts PruneOptions) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	if opts.Now.IsZero() {
		opts.Now = s.now()
	}
	if opts.Retention <= 0 {
		opts.Retention = ChangeRetention
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = ChangeMaxRows
	}
	if opts.Batch <= 0 {
		opts.Batch = PruneBatchSize
	}
	budget := int64(opts.Batch)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("controlsync: begin prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	floor, err := prunedThroughRev(ctx, tx)
	if err != nil {
		return 0, err
	}
	var headBefore int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(rev), 0) FROM changes`).Scan(&headBefore); err != nil {
		return 0, fmt.Errorf("controlsync: read head before prune: %w", err)
	}
	if headBefore < floor {
		headBefore = floor
	}

	var deleted int64
	cutoff := opts.Now.Add(-opts.Retention).UTC().Format(time.RFC3339Nano)
	for deleted < budget {
		want := budget - deleted
		n, err := deleteChangeBatch(ctx, tx, `created_at < ?`, cutoff, want)
		if err != nil {
			return 0, err
		}
		deleted += n
		// 删不满一批说明过期的已经删完，别再空转。
		if n < want {
			break
		}
	}
	if deleted < budget {
		var rows int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM changes`).Scan(&rows); err != nil {
			return 0, fmt.Errorf("controlsync: count changes: %w", err)
		}
		if excess := rows - int64(opts.MaxRows); excess > 0 {
			want := excess
			if room := budget - deleted; room < want {
				want = room
			}
			n, err := deleteChangeBatch(ctx, tx, `1 = 1`, nil, want)
			if err != nil {
				return 0, err
			}
			deleted += n
		}
	}

	// 下限取「仍然存在的最小 rev 减一」：删的是最旧的一批，所以它以下都已不在。
	// 表被删空时取清理前的 head —— 那是曾经发出去过的最大序号。
	newFloor := floor
	var minRemaining sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(rev) FROM changes`).Scan(&minRemaining); err != nil {
		return 0, fmt.Errorf("controlsync: read min rev: %w", err)
	}
	if minRemaining.Valid {
		if v := minRemaining.Int64 - 1; v > newFloor {
			newFloor = v
		}
	} else if headBefore > newFloor {
		newFloor = headBefore
	}
	if newFloor != floor {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO state(id, pruned_through_rev) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET pruned_through_rev = MAX(pruned_through_rev, excluded.pruned_through_rev)`,
			newFloor); err != nil {
			return 0, fmt.Errorf("controlsync: advance pruned_through_rev: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("controlsync: commit prune: %w", err)
	}
	return deleted, nil
}

// deleteChangeBatch 按 rev 子查询 + LIMIT 删最旧的一批。
// SQLite 默认构建不支持 `DELETE ... LIMIT`，限定条数只能靠子查询。
// where 只由本文件的常量构造，不带外部输入。
func deleteChangeBatch(ctx context.Context, tx *sql.Tx, where string, arg any, limit int64) (int64, error) {
	q := `DELETE FROM changes WHERE rev IN (
	SELECT rev FROM changes WHERE ` + where + ` ORDER BY rev LIMIT ?)`
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	args = append(args, limit)
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("controlsync: prune changes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("controlsync: prune changes: %w", err)
	}
	return n, nil
}
