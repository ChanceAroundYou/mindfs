package controlsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	// ChangePageDefault 是没带 limit 时的一页条数。
	ChangePageDefault = 200
	// ChangePageMax 是服务端一页的硬上限：边缘坏掉/被改动时也不许把主节点的
	// 变更流一次性全吐出去。
	ChangePageMax = 2000

	// SyncSchemaVersion 是同步通道的 schema 版本：载荷形状与库结构的总版本。
	// 两端不一致时合并会把对方不认识的形状写进权威存储，宁可拒绝。
	SyncSchemaVersion = 1
)

// SyncIdentity 是主节点通过 head 自报的身份。
//
// 边缘拿 InstanceID 判断「对面还是不是原来那台主节点」：主节点被重建/换机后
// instance_id 变化，边缘必须停下而不是把一台空库当成「主节点删光了数据」。
type SyncIdentity struct {
	InstanceID   string
	BuildVersion string
}

// HeadInfo 是 `GET /api/control-sync/head` 的响应载荷（spec §6）。
type HeadInfo struct {
	HeadRev       int64  `json:"head_rev"`
	InstanceID    string `json:"instance_id,omitempty"`
	BuildVersion  string `json:"build_version,omitempty"`
	SchemaVersion int    `json:"schema_version"`
}

// ChangeFeed 是主节点侧只读同步端点的实现：`GET /api/control-sync/head` 与
// `GET /api/control-sync/changes`。
//
// 它只读本机变更流，不碰权威存储 —— 权威存储由采集器（Capturer）写，两者唯一的
// 交集是同一个 sqlite 库。
type ChangeFeed struct {
	store *Store

	// identity 在装配时设一次，但 HTTP 处理是并发的，读侧仍要锁。
	mu       sync.RWMutex
	identity SyncIdentity
}

func NewChangeFeed(store *Store) (*ChangeFeed, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	return &ChangeFeed{store: store}, nil
}

// SetIdentity 设置本机（主节点）的对外身份。没设或留空时 head 如实报空：
// 边缘据此跳过身份比对，而不是拿一个编造的 id 把真正的替换盖住。
func (f *ChangeFeed) SetIdentity(id SyncIdentity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity = id
}

func (f *ChangeFeed) identitySnapshot() SyncIdentity {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.identity
}

// Head 返回变更流水位与本机身份。
func (f *ChangeFeed) Head(ctx context.Context) (HeadInfo, error) {
	rev, err := f.store.HeadRev(ctx)
	if err != nil {
		return HeadInfo{}, err
	}
	id := f.identitySnapshot()
	return HeadInfo{
		HeadRev:       rev,
		InstanceID:    id.InstanceID,
		BuildVersion:  id.BuildVersion,
		SchemaVersion: SyncSchemaVersion,
	}, nil
}

// Page 返回 since 之后的一页变更与当时的 head_rev。
//
// head_rev 与 changes 取自两处（不可能同一个快照），所以它只是「拉取方看到这一页时
// 主节点到了哪」的提示；判追平要用下一页是否为空，不能拿 rev 相等当收敛条件。
func (f *ChangeFeed) Page(ctx context.Context, since int64, limit int) ([]RemoteChange, int64, error) {
	if since < 0 {
		since = 0
	}
	if limit <= 0 {
		limit = ChangePageDefault
	}
	if limit > ChangePageMax {
		limit = ChangePageMax
	}
	// since 是闭区间下界：它落在已清理区间里（<= 下限）就意味着这位拉取方需要的
	// 变更已经被删掉了。此时返回一页「看起来正常」的变更是最坏的选择 ——
	// 副本会永久缺一段控制面，而两边都没有任何异常信号。
	floor, err := f.store.PrunedThroughRev(ctx)
	if err != nil {
		return nil, 0, err
	}
	if floor > 0 && since <= floor {
		return nil, 0, fmt.Errorf("%w: since=%d pruned_through=%d", ErrRetentionGap, since, floor)
	}
	changes, err := f.store.ListChangesSince(ctx, since, limit)
	if err != nil {
		return nil, 0, err
	}
	head, err := f.store.HeadRev(ctx)
	if err != nil {
		return nil, 0, err
	}
	return changes, head, nil
}
