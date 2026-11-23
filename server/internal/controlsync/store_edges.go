package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrEdgeRevoked 表示这台边控端已被主节点踢掉：它的同步数据面被拒绝，
// 探活仍然放行（页面与转发不跟着断，故障才看得见）。
var ErrEdgeRevoked = errors.New("control_sync: edge revoked")

// ErrEdgeNotFound 表示要操作的那台边控端从没出现过。
// 导出是因为 HTTP 层要把它翻成 404（踢一台从没连上来的机器不是成功）。
var ErrEdgeNotFound = errors.New("control_sync: edge not registered")

// EdgeInfo 是主节点记住的一台边控端（edge）。
//
// 「注册」发生在第一次通过签名校验的同步请求上，不另设注册端点：边缘探活打
// /head、追平打 /changes、推送打 /push，任何一条能过 token 校验的请求都已经证明
// 它持有同一份凭据 —— 再要求它额外调一次 /register 只是多一次可能失败的网络往返。
//
// 必须说清边界：`X-MindFS-Instance` 是调用方的**自称**，不在签名覆盖范围内
// （见 auth.go），所以这里的记录只是运维索引。踢人（Revoke）让「这台机器别再同步」
// 成为可执行的动作，但它挡不住一个换名字重新接入的客户端 —— 真正的凭证始终是 token。
type EdgeInfo struct {
	InstanceID   string    `json:"instance_id"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	// RevokedAt 非 nil 表示已被踢掉；恢复（Restore）会把它清回 nil。
	// 用指针而不是零值时间：零值会序列化成 "0001-01-01T00:00:00Z"，
	// 前端与人都要额外判断一次才知道那不是个真时刻。
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Revoked 是给读端用的便利判断。
func (e EdgeInfo) Revoked() bool { return e.RevokedAt != nil }

// ObserveEdge 记一次来自某台边控端的同步请求，返回它当前的状态。
//
// 幂等：已登记的只推进 last_seen_at；被踢掉的返回 ErrEdgeRevoked 且**不改任何行**，
// 否则对方下一次轮询就会把自己的封禁覆盖回来。
func (s *Store) ObserveEdge(ctx context.Context, instanceID string, at time.Time) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return EdgeInfo{}, fmt.Errorf("controlsync: edge instance id required")
	}
	if at.IsZero() {
		at = s.now()
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO edges(instance_id, registered_at, last_seen_at)
VALUES(?, ?, ?)
ON CONFLICT(instance_id) DO UPDATE SET last_seen_at = excluded.last_seen_at
WHERE edges.revoked_at = ''`, instanceID, stamp, stamp); err != nil {
		return EdgeInfo{}, fmt.Errorf("controlsync: observe edge: %w", err)
	}
	edge, err := s.edgeByID(ctx, instanceID)
	if errors.Is(err, ErrEdgeNotFound) {
		// 上面那条 upsert 的 WHERE 让被踢掉的行原地不动，因此走到这里说明它还在。
		return EdgeInfo{}, fmt.Errorf("controlsync: observe edge %q: %w", instanceID, err)
	}
	if err != nil {
		return EdgeInfo{}, err
	}
	if edge.Revoked() {
		return edge, ErrEdgeRevoked
	}
	return edge, nil
}

// ListEdges 按登记时间返回所有出现过的边控端（最新的在前）。
func (s *Store) ListEdges(ctx context.Context) ([]EdgeInfo, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT instance_id, registered_at, last_seen_at, revoked_at FROM edges
ORDER BY registered_at DESC, instance_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list edges: %w", err)
	}
	defer rows.Close()
	var out []EdgeInfo
	for rows.Next() {
		edge, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

// RevokeEdge 踢掉一台边控端。已经踢过的保持幂等（只刷时间）。
func (s *Store) RevokeEdge(ctx context.Context, instanceID string, at time.Time) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return EdgeInfo{}, fmt.Errorf("controlsync: edge instance id required")
	}
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE edges SET revoked_at = ? WHERE instance_id = ? AND revoked_at = ''`,
		at.UTC().Format(time.RFC3339Nano), instanceID); err != nil {
		return EdgeInfo{}, fmt.Errorf("controlsync: revoke edge: %w", err)
	}
	return s.edgeByID(ctx, instanceID)
}

// RestoreEdge 撤回踢人：这台边控端下次请求起重新同步。
//
// 单独一个动作而不是「再登记一次」：注册是自动的，把恢复也做成自动的就等于
// 踢人没有意义了。
func (s *Store) RestoreEdge(ctx context.Context, instanceID string) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return EdgeInfo{}, fmt.Errorf("controlsync: edge instance id required")
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE edges SET revoked_at = '' WHERE instance_id = ?`, instanceID); err != nil {
		return EdgeInfo{}, fmt.Errorf("controlsync: restore edge: %w", err)
	}
	return s.edgeByID(ctx, instanceID)
}

// EdgeByID 按 instance id 取一台边控端（不存在时返回包装过的 ErrEdgeNotFound）。
func (s *Store) EdgeByID(ctx context.Context, instanceID string) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	return s.edgeByID(ctx, strings.TrimSpace(instanceID))
}

func (s *Store) edgeByID(ctx context.Context, instanceID string) (EdgeInfo, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT instance_id, registered_at, last_seen_at, revoked_at FROM edges WHERE instance_id = ?`, instanceID)
	edge, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EdgeInfo{}, fmt.Errorf("%w: %q", ErrEdgeNotFound, instanceID)
	}
	return edge, err
}

// edgeScanner 让 QueryRow 与 Rows 共用一份扫描逻辑，避免两处对列序各写一遍。
type edgeScanner interface {
	Scan(dest ...any) error
}

func scanEdge(row edgeScanner) (EdgeInfo, error) {
	var (
		edge       EdgeInfo
		registered string
		lastSeen   string
		revoked    string
	)
	if err := row.Scan(&edge.InstanceID, &registered, &lastSeen, &revoked); err != nil {
		return EdgeInfo{}, err
	}
	edge.RegisteredAt, _ = time.Parse(time.RFC3339Nano, registered)
	edge.LastSeenAt, _ = time.Parse(time.RFC3339Nano, lastSeen)
	if revoked != "" {
		if at, err := time.Parse(time.RFC3339Nano, revoked); err == nil {
			edge.RevokedAt = &at
		}
	}
	return edge, nil
}
