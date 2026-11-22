package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/preferences"
)

// ControlSyncOptions 是控制面同步的启动参数（config.json 的 control_sync 段）。
//
// Token 只允许由 token_file 读出来再传进来：复制的是整份控制面（含口令 hash），
// 密钥不进 config.json、不进进程参数、不进日志。
type ControlSyncOptions struct {
	Enabled    bool
	Role       string // primary（默认）| edge
	InstanceID string
	PrimaryURL string
	CAFile     string
	Token      string
	// BuildVersion 是本进程的版本号，随 head 下发供对端诊断（不参与拒绝判定）。
	BuildVersion string
}

// controlSyncStores 是同步域赖以读写的既有权威存储。nil 表示那份存储没建起来。
type controlSyncStores struct {
	auth      *auth.Store
	prefs     *preferences.Store
	nodes     *nodes.Store
	workflows *kanban.WorkflowStore
}

// controlSyncWiring 是接线产物：运行期与 HTTP 端点必须共用这一份，
// 否则 store 的抑制计数与采集器会对不上。
type controlSyncWiring struct {
	runtime  *controlsync.Runtime
	capturer *controlsync.Capturer

	service   api.ControlSyncService
	pusher    api.ControlSyncPusher
	feed      api.ControlSyncFeed
	conflicts api.ControlSyncConflicts
	// token 只有主节点非空：边缘没有 push / head / changes 端点。
	token string
	// proxy 只有边缘非空：主节点不转发。
	proxy *api.ControlProxy
}

// applyTo 把同步能力装进按账户构建的 HTTP handler。
func (w *controlSyncWiring) applyTo(h *api.HTTPHandler) {
	if w == nil || h == nil {
		return
	}
	h.ControlSync = w.service
	h.ControlSyncPush = w.pusher
	h.ControlSyncFeed = w.feed
	h.ControlSyncConflicts = w.conflicts
	h.ControlSyncToken = w.token
}

// wrapRoot 按角色包一层中间件：采集在内、转发在外。
//
// 顺序不能反：转发出去的控制面请求不该在本机留下任何记录（边缘在线时本地权威
// 存储本来就不该变）。转发层只在 online 才真的转发，其余模式把请求交回本地副本，
// 写入是否冻结由 ControlProxy 按域判定。
func (w *controlSyncWiring) wrapRoot(next http.Handler) http.Handler {
	if w == nil {
		return next
	}
	root := w.capturer.WriteCapture(next)
	if w.proxy != nil {
		root = w.proxy.Handler(root)
	}
	return root
}

// startControlSync 组装控制面同步。opts 为 nil / 未启用时返回 (nil, nil)：
// 这台机器保持改造前的行为，不建库也不起任何后台循环。
func startControlSync(ctx context.Context, opts *ControlSyncOptions, nodeRole nodeinfo.Role, configDir string, stores controlSyncStores) (*controlSyncWiring, error) {
	if opts == nil || !opts.Enabled {
		return nil, nil
	}
	if nodeRole == nodeinfo.RoleWorker {
		return nil, fmt.Errorf("control_sync: node role is worker, control sync needs role=control")
	}
	cfg := controlsync.Config{
		Enabled:      true,
		Role:         opts.Role,
		InstanceID:   opts.InstanceID,
		BuildVersion: opts.BuildVersion,
		PrimaryURL:   opts.PrimaryURL,
		CAFile:       opts.CAFile,
		Token:        opts.Token,
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	cfg = normalized
	if cfg.InstanceID == "" {
		// 主节点也记 origin（变更流里的来源标签）。取主机名，别拿空串。
		cfg.InstanceID, _ = os.Hostname()
	}
	if cfg.Role == controlsync.RolePrimary && cfg.Token == "" {
		// 没有 token 的 primary 不会注册 /push /head /changes，边缘永远同步不了 ——
		// 那不是「少个可选配置」，是同步根本没通，必须在启动时说出来。
		return nil, fmt.Errorf("control_sync: sync token required for primary (token_file)")
	}

	domains, err := controlSyncDomains(configDir, stores)
	if err != nil {
		return nil, err
	}
	registry, err := controlsync.NewRegistry(domains...)
	if err != nil {
		return nil, err
	}
	store, err := controlsync.Open(filepath.Join(configDir, "control_sync.db"))
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	capturer, err := controlsync.NewCapturer(store, registry, cfg.InstanceID)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	capturer.OnCaptureError = func(err error) { log.Printf("[control-sync] capture.error err=%v", err) }
	cfg.OnError = func(err error) { log.Printf("[control-sync] error err=%v", err) }
	rt, err := controlsync.NewRuntime(ctx, cfg, controlsync.Deps{
		Store:    store,
		Registry: registry,
		Capturer: capturer,
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if cfg.Role == controlsync.RolePrimary {
		// 启动即把整份控制面写进变更流。兜底扫描要 15 秒才有第一拍，而边缘完全
		// 可能在这之前完成首次追平然后掉线 —— 那一版副本会缺一批记录（首次启动
		// 生成的管理员账户就在其中），恢复时把主节点早就有的记录当成「两侧都改」
		// 报冲突。写失败只记日志，兜底扫描会补。
		if n, err := capturer.CaptureAll(ctx); err != nil {
			log.Printf("[control-sync] baseline.capture.error err=%v", err)
		} else {
			log.Printf("[control-sync] baseline published records=%d", n)
		}
	}

	w := &controlSyncWiring{
		runtime:   rt,
		capturer:  capturer,
		conflicts: rt.Resolver(),
	}
	if cfg.Role == controlsync.RolePrimary {
		w.pusher = rt.Pusher()
		w.feed = rt.Feed()
		w.token = cfg.Token
	}
	if mode := rt.Mode(); mode != nil {
		// 边缘：状态/模式端点 + 转发器。转发器只有在 online 时真的转发，
		// 其余模式交给本地副本，写请求按域冻结由阻塞判定兜底。
		w.service = mode
		proxy, err := api.NewControlProxy(api.ControlProxyOptions{
			PrimaryURL:  cfg.PrimaryURL,
			CAFile:      cfg.CAFile,
			ForwardedBy: cfg.InstanceID,
		})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		proxy.Mode = mode.Mode
		proxy.BlockedDomain = blockedDomainChecker(store)
		w.proxy = proxy
	}
	log.Printf("[control-sync] enabled role=%s instance=%s primary=%s",
		cfg.Role, cfg.InstanceID, cfg.PrimaryURL)
	return w, nil
}

// controlSyncDomains 组装六个同步域。启用同步却建不出某个域时直接失败：
// 缺域的副本看起来「能用」，直到主节点恢复后才发现一半控制面根本没跟上。
func controlSyncDomains(configDir string, stores controlSyncStores) ([]controlsync.Domain, error) {
	domains := make([]controlsync.Domain, 0, 6)
	for name, ok := range map[string]bool{
		"users":       stores.auth != nil,
		"preferences": stores.prefs != nil,
		"nodes":       stores.nodes != nil,
		"workflows":   stores.workflows != nil,
	} {
		if !ok {
			return nil, fmt.Errorf("control_sync: %s store unavailable", name)
		}
	}
	prompts, err := controlsync.NewPromptsDomain(configDir)
	if err != nil {
		return nil, err
	}
	pins, err := controlsync.NewPinsDomain(configDir)
	if err != nil {
		return nil, err
	}
	return append(domains,
		controlsync.NewUsersDomain(stores.auth),
		controlsync.NewPreferencesDomain(stores.prefs),
		controlsync.NewNodesDomain(stores.nodes),
		controlsync.NewWorkflowsDomain(stores.workflows),
		prompts,
		pins,
	), nil
}

// blockedDomainChecker 回答「这个域是不是因为未裁决的冲突被冻结」。
//
// 只在 reconciling / conflict-review 下被问到，控制面写入本来就稀少，直接查库
// 保证判定始终是当下的事实；查不出来按冻结处理（fail closed）—— 恢复期宁可
// 让用户重试一次写入，也不要让本地改动和主节点分叉。
func blockedDomainChecker(store *controlsync.Store) func(string) bool {
	return func(domain string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cards, err := store.ListOpenConflicts(ctx)
		if err != nil {
			log.Printf("[control-sync] blocked.domain.error err=%v", err)
			return true
		}
		for _, card := range cards {
			if card.Domain == domain {
				return true
			}
		}
		return false
	}
}
