package app

import (
	"context"
	"fmt"

	"mindfs/server/internal/controlsync"
)

// guardSinglePrimary 回答「这台机器能不能当主控」，并且只用库里已有的证据。
//
// 唯一写者（D5）是这条通道的前提：两个 primary 各自往自己的变更流里追加 rev，
// 边缘拉到谁的就是谁的那半份控制面，两侧都以为自己是对的。所以启动前必须拦掉
// 「这本来就不是主控」的机器。
//
// 判据只认 state.primary_instance_id：全仓唯一的写者是 controlsync.Replicator，
// 而 Replicator 只在边缘构造。主节点启动时这一列非空 ⇒ 这份 control_sync.db
// 曾经是别人家的副本，拿它当主控就是两个写者。没有这条判据时，把边缘机器上那份
// 库连同 config.json 一起复制到新机器、或者边缘改配置 rising 成主控，都会静默
// 生效 —— 症状是边缘拉到一半的 rev 序列，而双方日志里都写着 enabled role=primary。
//
// 不改库：这里只读不写。解绑走边缘那套 adopt_primary（ResetReplicaForAdopt 会把
// 这一列清空），所以「先在这台机器上接受新主节点」是唯一出路，不是死路。
func guardSinglePrimary(ctx context.Context, store *controlsync.Store, cfg controlsync.Config) error {
	if cfg.Role != controlsync.RolePrimary {
		return nil
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		// 读不出状态就不许起：读失败时它可能是「空」，也可能是「曾是谁的副本」。
		// 把未知当空，正是两个写者的起步方式。
		return fmt.Errorf("control_sync: read state before acting as primary: %w", err)
	}
	if state.PrimaryInstanceID != "" {
		return fmt.Errorf(
			"control_sync: this control_sync.db was a replica of %q, refusing to act as primary "+
				"(two writers would fork the control plane); on that machine run "+
				"POST /api/control-sync/mode {\"action\":\"adopt_primary\"} or delete control_sync.db first",
			state.PrimaryInstanceID)
	}
	return nil
}
