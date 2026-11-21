package controlsync

import (
	"context"
	"errors"
)

const (
	// ChangePageDefault 是没带 limit 时的一页条数。
	ChangePageDefault = 200
	// ChangePageMax 是服务端一页的硬上限：边缘坏掉/被改动时也不许把主节点的
	// 变更流一次性全吐出去。
	ChangePageMax = 2000
)

// ChangeFeed 是主节点侧只读同步端点的实现：`GET /api/control-sync/head` 与
// `GET /api/control-sync/changes`。
//
// 它只读本机变更流，不碰权威存储 —— 权威存储由采集器（Capturer）写，两者唯一的
// 交集是同一个 sqlite 库。
type ChangeFeed struct {
	store *Store
}

func NewChangeFeed(store *Store) (*ChangeFeed, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	return &ChangeFeed{store: store}, nil
}

// Head 返回变更流当前水位。
func (f *ChangeFeed) Head(ctx context.Context) (int64, error) {
	return f.store.HeadRev(ctx)
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
