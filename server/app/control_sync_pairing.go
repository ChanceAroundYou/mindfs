package app

import (
	"context"
	"log"

	"mindfs/server/internal/api"
	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/nodeinfo"
)

// controlSyncPairer 把「接到主控端」这件事接到 controlsync.Pair 上。
//
// 它只持有两个路径：token 落到哪、config.json 在哪。主控端的地址与密钥都来自请求体，
// 因为这台机器还没有它们的任何一份副本。
type controlSyncPairer struct {
	tokenFile  string
	configPath string
	instanceID string
}

// Pair 是 api.ControlSyncPairing 的实现。
func (p controlSyncPairer) Pair(ctx context.Context, req controlsync.PairRequest) (controlsync.PairResult, error) {
	return controlsync.Pair(ctx, req, p.tokenFile, p.configPath)
}

// PairingState 是 api.ControlSyncPairing 的实现。
//
// 挂出这个接口的机器只可能是「还没配好的边缘」，所以 pending 恒为真；带上实例名是
// 为了让表单预填、并在主控端的 `/edges` 名单里认得出来。
func (p controlSyncPairer) PairingState() api.PairingState {
	return api.PairingState{PendingPairing: true, InstanceID: p.instanceID}
}

// newControlSyncPairer 在缺路径时返回 nil（等于不挂端点）。
//
// 没有 config.json 路径就写不回落盘配置，配完重启还是未配对 —— 那比不提供入口更糟：
// 用户会得到一次「配对成功」的提示，然后看着机器回到原样。所以宁可少这个入口。
func newControlSyncPairer(opts *ControlSyncOptions) api.ControlSyncPairing {
	if opts == nil {
		return nil
	}
	if opts.ConfigPath == "" || opts.TokenFile == "" {
		log.Printf("[control-sync] pairing.disabled reason=missing_paths config=%q token_file=%q",
			opts.ConfigPath, opts.TokenFile)
		return nil
	}
	return controlSyncPairer{tokenFile: opts.TokenFile, configPath: opts.ConfigPath, instanceID: opts.InstanceID}
}

// pendingPairingWiring 是未配对边缘的接线：只有配对端点，没有同步、采集、转发。
//
// 这台机器此时仍是完整的 control 节点（出页面、跑本机 agent），只是控制面只有本机
// 这一份、还没开始跟随任何主控端。
func pendingPairingWiring(opts *ControlSyncOptions, nodeRole nodeinfo.Role) *controlSyncWiring {
	if nodeRole == nodeinfo.RoleWorker {
		// worker 不持有控制面、也不出页面，配对在它身上没有意义。
		return nil
	}
	pairing := newControlSyncPairer(opts)
	if pairing == nil {
		return nil
	}
	log.Printf("[control-sync] awaiting pairing instance=%s — open this machine's page to connect it to a primary",
		opts.InstanceID)
	return &controlSyncWiring{pairing: pairing}
}
