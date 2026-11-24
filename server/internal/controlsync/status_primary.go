package controlsync

import (
	"context"
	"fmt"
	"time"
)

// PrimaryStatus 是主节点视角的同步状态摘要。
//
// 边缘的 Status 回答的是「我这一侧处在哪个模式、还差多少追平」，主节点没有模式，
// 它要回答的是另一组问题：变更流水位推到哪了、有几台边控端在跟着、最近一次心跳
// 是什么时候、有没有待裁决的冲突。两者共用 /api/control-sync/status 这一个地址，
// 靠 role 字段区分 —— 前端只认 role，不猜「为什么没有 mode」。
//
// 这里刻意只放计数与时间：不放 instance_id 名单，也不放任何记录载荷。这条端点在
// 浏览器里要被无 token 读到（横幅要显示它），而主节点是公网可达的。
type PrimaryStatus struct {
	Role string `json:"role"` // 恒为 "primary"，与边缘的四态字符串区分开

	HeadRev       int64 `json:"head_rev"`
	SchemaVersion int   `json:"schema_version"`

	EdgeCount       int        `json:"edge_count"`
	ActiveEdgeCount int        `json:"active_edge_count"`
	LastEdgeSeenAt  *time.Time `json:"last_edge_seen_at,omitempty"`

	ConflictCount int `json:"conflict_count"`

	// CheckedAt 是这份摘要的生成时刻。没有它，读者无法分辨「同步真的停了」
	// 与「这一份是缓存」—— 前端要拿它和浏览器时钟对一眼。
	CheckedAt time.Time `json:"checked_at"`
}

// PrimaryStatus 汇总主节点的水位、边控端概况与未裁决冲突数。
//
// 三项各自查一次库：任一项读失败就整体失败。半份摘要比没有摘要更糟 ——
// 缺了 conflict_count 的「一切正常」会让人以为没有待裁决项。
func (s *Store) PrimaryStatus(ctx context.Context, now time.Time) (PrimaryStatus, error) {
	if s == nil || s.db == nil {
		return PrimaryStatus{}, fmt.Errorf("controlsync: store closed")
	}
	head, err := s.HeadRev(ctx)
	if err != nil {
		return PrimaryStatus{}, fmt.Errorf("controlsync: primary status head: %w", err)
	}
	edges, err := s.ListEdges(ctx)
	if err != nil {
		return PrimaryStatus{}, fmt.Errorf("controlsync: primary status edges: %w", err)
	}
	conflicts, err := s.CountOpenConflicts(ctx)
	if err != nil {
		return PrimaryStatus{}, fmt.Errorf("controlsync: primary status conflicts: %w", err)
	}

	st := PrimaryStatus{
		Role:          "primary",
		HeadRev:       head,
		SchemaVersion: SyncSchemaVersion,
		EdgeCount:     len(edges),
		ConflictCount: conflicts,
		CheckedAt:     now.UTC(),
	}
	for _, e := range edges {
		if e.RevokedAt != nil {
			continue
		}
		st.ActiveEdgeCount++
		seen := e.LastSeenAt.UTC()
		if st.LastEdgeSeenAt == nil || seen.After(*st.LastEdgeSeenAt) {
			t := seen
			st.LastEdgeSeenAt = &t
		}
	}
	return st, nil
}
