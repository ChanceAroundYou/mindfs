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

// ErrRelayNameTaken 表示要落的名字已被另一台边控端占用。
// 地址必须一台机器唯一：共用会让请求随机落到其中一台。
var ErrRelayNameTaken = errors.New("control_sync: relay name already taken")

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
	// RelayName 是边控端在 /mindfs/relay/<name> 下对外使用的名字。
	//
	// 由主控分配并落库（见 RelayHub）：其他设备同步到的节点地址里就带着它，
	// 所以它必须稳定 —— 重连、主控重启都不能改名。空串表示还没建过隧道。
	RelayName string `json:"relay_name,omitempty"`
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
	edge, err := s.EdgeByID(ctx, instanceID)
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
SELECT instance_id, registered_at, last_seen_at, relay_name, revoked_at FROM edges
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
	return s.edgeByID(ctx, instanceID)
}

func (s *Store) edgeByID(ctx context.Context, instanceID string) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT instance_id, registered_at, last_seen_at, relay_name, revoked_at FROM edges WHERE instance_id = ?`, strings.TrimSpace(instanceID))
	edge, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EdgeInfo{}, fmt.Errorf("%w: %q", ErrEdgeNotFound, instanceID)
	}
	return edge, err
}

// EdgeByRelayName 按对外名字反查一台边控端（relay 入口用）。
//
// 找不到返回包装过的 ErrEdgeNotFound：relay 路径里的名字是外部输入，调用方要能
// 把它翻成 404 而不是 500。
func (s *Store) EdgeByRelayName(ctx context.Context, name string) (EdgeInfo, error) {
	if s == nil || s.db == nil {
		return EdgeInfo{}, errors.New("controlsync: store closed")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return EdgeInfo{}, fmt.Errorf("%w: empty relay name", ErrEdgeNotFound)
	}
	row := s.db.QueryRowContext(ctx, `
SELECT instance_id, registered_at, last_seen_at, relay_name, revoked_at FROM edges WHERE relay_name = ?`, name)
	edge, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EdgeInfo{}, fmt.Errorf("%w: relay name %q", ErrEdgeNotFound, name)
	}
	return edge, err
}

// SetRelayName 给一台边控端落对外名字。已登记但名字不同时**覆盖**：
// 分配规则只由主控决定（见 RelayHub），边控端上报的只是候选名。
//
// 名字是其他设备唯一能拼出访问地址的东西，因此它必须唯一：
//   - 未登记的 edge 不能赋名字（会写出一个无处可解析的地址）；
//   - 已被别的 edge 占着的名字不能抢（两台机器共用一个地址 = 请求随机落到其中一台）。
func (s *Store) SetRelayName(ctx context.Context, instanceID, name string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	instanceID = strings.TrimSpace(instanceID)
	name = strings.TrimSpace(name)
	if instanceID == "" || name == "" {
		return errors.New("controlsync: instance id and relay name required")
	}
	if _, err := s.EdgeByID(ctx, instanceID); err != nil {
		return err
	}
	var owner string
	err := s.db.QueryRowContext(ctx,
		`SELECT instance_id FROM edges WHERE relay_name = ? AND instance_id <> ?`,
		name, instanceID).Scan(&owner)
	if err == nil {
		return fmt.Errorf("%w: relay name %q taken by %s", ErrRelayNameTaken, name, owner)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("controlsync: check relay name: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE edges SET relay_name = ? WHERE instance_id = ?`, name, instanceID); err != nil {
		return fmt.Errorf("controlsync: set relay name: %w", err)
	}
	return nil
}

// RelayNames 返回已占用的名字及归属，供分配时判重。
func (s *Store) RelayNames(ctx context.Context) (map[string]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT instance_id, relay_name FROM edges WHERE relay_name <> ''`)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list relay names: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
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
	if err := row.Scan(&edge.InstanceID, &registered, &lastSeen, &edge.RelayName, &revoked); err != nil {
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
