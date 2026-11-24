package app

import (
	"context"
	"log"
	"mindfs/server/internal/agent/fold"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/internal/codexsync"
	"mindfs/internal/deploy"
	"mindfs/server/internal/agent"
	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/config"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

const staticDirEnvKey = "MINDFS_STATIC_DIR"

type StartOptions struct {
	Version         string
	Args            []string
	AgentConfigPath string
	WebPushEnabled  bool
	// WebPushRelayURL 非空时，本节点把通知交给控制节点代发（worker 用）。
	// 控制节点自己不配置：它就是中继的接收方。
	WebPushRelayURL string
	NotifyScript    string
	UseTLS          bool
	CertFile        string
	KeyFile         string
	// Role 决定这台机器提不提供控制面与前端。空 = control（改造前的行为）。
	//
	// 用 string 而不是 nodeinfo.Role：真正的入口 cli/cmd 在根 module，
	// import 不到 server/internal/nodeinfo（Go internal 规则），
	// 而它是唯一需要设置这个字段的地方。值在使用点统一 Normalize。
	Role string
	// Feishu 是外部通道（飞书）的本机配置。Enabled=false（缺省）时通道不创建。
	Feishu FeishuOptions
	// 根级飞书中继配置：FeishuRelayURL 是 worker 把卡片/回答交给控制节点的地址，
	// FeishuNodeID 是本节点在飞书项目目录里的名字。两者只走 config.json。
	FeishuRelayURL string
	FeishuNodeID   string
	// Task Router 是节点本地配置；cli/cmd 不能 import server/internal/kanban，
	// 所以入口只传字符串/时长，在 Start 里归一化成 kanban.RouterConfig。
	RouterMode        string
	RouterAgent       string
	RouterModel       string
	RouterEffort      string
	RouterTurnTimeout time.Duration
	RouterIdleClose   time.Duration
	RouterRoots       map[string]string
	// RouterSupervision* 是主动监管的开关与预算（config.json 的 router.supervision）。
	// 空模式 = off；预算 <=0 由 kanban 归一化成默认值。
	RouterSupervisionMode              string
	RouterSupervisionMaxRetriesPerNode int
	RouterSupervisionMaxActionsPerRun  int
	RouterSupervisionMaxActionsPerHour int
	RouterSupervisionQuotaHoldSeconds  int
	// TaskAgent / TaskModel 是新任务派发的默认 Agent 与模型（节点本地
	// config.json 的 task 段）。空值 = 内建 claude + sonnet，见
	// taskConfigFromOptions。
	TaskAgent string
	TaskModel string
	// WorkflowParallelMainTree 控制没有独立 worktree 的任务是否共用主树并行写。
	// 入口 flag 默认 true；config.json 的 workflow.parallel_main_tree 可关闭。
	WorkflowParallelMainTree bool
	// LiveCompact 是节点本地的上下文活压缩配置。空值 = off。
	// 入口传字符串/整数，app 再转成 proxy.LiveCompactConfig，原因同 Router。
	LiveCompactMode             string
	LiveCompactKeepRounds       int
	LiveCompactKeepTailTokens   int
	LiveCompactMinBlockTokens   int
	LiveCompactMinBodyBytes     int
	LiveCompactMaxDriftMessages int
	// Rail gate and fold budget (design §12.7). Zero values mean the proxy
	// defaults; a negative LiveCompactRailTokens disables the gate.
	LiveCompactRailTokens       int
	LiveCompactMinFoldBytes     int
	LiveCompactMaxFoldBytes     int
	LiveCompactLargeBlockTokens int
	// LiveCompactDisableFile 是急停文件；空值 = <config-dir>/context/DISABLED。
	LiveCompactDisableFile string
	// LiveCompactArchiveDir 是折叠存档根；空值 = <config-dir>/context。
	LiveCompactArchiveDir string
	// LiveCompactPrefixGuard 是 preserved-thinking 前缀闸门；nil = 开。
	LiveCompactPrefixGuard        *bool
	LiveCompactPrefixUnsafeModels []string
	// LiveCompactColdFold 是冷缓存择时折叠；nil = 开。
	LiveCompactColdFold        *bool
	LiveCompactColdRailTokens  int
	LiveCompactCacheTTLSeconds int
	// LiveCompactHarnesses 按 harness 覆盖上面的策略（`live_compact.harness.*`）。
	// nil = 所有 harness 都用节点配置。
	LiveCompactHarnesses map[string]LiveCompactHarnessOverride
	// ControlSync 是控制面同步（离线优先本地控制面）。nil / Enabled=false 时
	// 一切保持改造前的行为：不建库、不采集、不转发、端点 404。
	ControlSync *ControlSyncOptions
	// SessionHygiene 是会话卫生策略（config.json 的 session_hygiene）。零值 =
	// 默认策略（开启），见 sessionHygieneConfigFromOptions。
	SessionHygiene SessionHygieneOptions
	// UpdateAutoInstall 打开静默自动更新（config.json 的 update.auto_install）。
	// 默认 false，且只该由 Windows 独立安装包打开：装完会重启进程，而 Unix 的
	// systemd 单元是 Restart=on-failure + 替换进程 setsid 脱离 cgroup ——
	// 静默重启等于让服务从 systemd 眼里消失，还会腰斩正在跑的 agent 回合。
	UpdateAutoInstall bool
}

// SessionHygieneOptions 是节点本地的超长会话提示策略。它只影响本机，不走控制面。
//
// 零值不是「关闭」而是「默认值」：这个特性按用户裁定默认开启，要关必须显式写
// enabled=false。时长上限等以秒为单位，0 = 用默认值。
type SessionHygieneOptions struct {
	Enabled        *bool
	ActiveSeconds  int
	ContextPercent int
	OverLineTurns  int
	IdleGapSeconds int
	Harnesses      map[string]SessionHygieneHarnessOverride
}

// SessionHygieneHarnessOverride 按 harness 覆盖全局阈值；0 = 沿用全局值。
type SessionHygieneHarnessOverride struct {
	ActiveSeconds  int
	ContextPercent int
	OverLineTurns  int
	IdleGapSeconds int
}

// LiveCompactHarnessOverride 是 fold.HarnessOverride 的可导入别名。
// `cli/cmd` 不在 server/ 下，不能直接引用 internal 包，但它正是 `make build`
// 安装的那个命令，所以这条配置只能经由本别名交到它手里。
type LiveCompactHarnessOverride = fold.HarnessOverride

// taskConfig 是新任务派发的默认 Agent/模型。它是节点本地配置，不进控制面：
// 每台机器装了什么 Agent 各不相同。
type taskConfig struct {
	Agent string
	Model string
}

// taskConfigFromOptions 归一化任务派发默认值。未配置 = claude + sonnet：
// 选 Agent 原本退回「探针顺序里第一个可用」，llmux 恢复后 codex 先可用，
// 新任务就整批落到了 codex（2026-11-20 事故）。显式配了 Agent 而没配模型时
// 模型留空，用该 Agent 自己的默认，不硬塞 sonnet。
func taskConfigFromOptions(opts StartOptions) taskConfig {
	agent := strings.TrimSpace(opts.TaskAgent)
	model := strings.TrimSpace(opts.TaskModel)
	if agent == "" {
		agent = "claude"
		if model == "" {
			model = "sonnet"
		}
	}
	return taskConfig{Agent: agent, Model: model}
}

// routerConfigFromOptions 是 string -> kanban.RouterConfig 的唯一转换点。
// 空 mode 等于 off：未配置 Router 的机器不会创建任何 Router 会话/后台任务。
func routerConfigFromOptions(opts StartOptions) kanban.RouterConfig {
	roots := make(map[string]kanban.RouterMode, len(opts.RouterRoots))
	for rootID, mode := range opts.RouterRoots {
		roots[rootID] = kanban.RouterMode(mode)
	}
	return kanban.RouterConfig{
		Mode:        kanban.RouterMode(opts.RouterMode),
		Agent:       opts.RouterAgent,
		Model:       opts.RouterModel,
		Effort:      opts.RouterEffort,
		TurnTimeout: opts.RouterTurnTimeout,
		IdleClose:   opts.RouterIdleClose,
		RootModes:   roots,
		Supervision: kanban.RouterSupervisionConfig{
			Mode:              kanban.RouterSupervisionMode(opts.RouterSupervisionMode),
			MaxRetriesPerNode: opts.RouterSupervisionMaxRetriesPerNode,
			MaxActionsPerRun:  opts.RouterSupervisionMaxActionsPerRun,
			MaxActionsPerHour: opts.RouterSupervisionMaxActionsPerHour,
			QuotaHold:         time.Duration(opts.RouterSupervisionQuotaHoldSeconds) * time.Second,
		},
	}.Normalized()
}

// newWaitAwareServer 构造 HTTP server，并让每个请求的上下文派生自一个可取消的
// BaseContext。长轮询端点会让 handler 阻塞十几秒；关停时只调 Shutdown 的话，
// 它要等这些 handler 自然结束，部署/重启就被拖慢。返回的 cancelWait 在 Shutdown
// 之前调用即可让在途请求立刻返回。
func newWaitAwareServer(addr string, handler http.Handler) (*http.Server, context.CancelFunc) {
	waitCtx, cancelWait := context.WithCancel(context.Background())
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return waitCtx
		},
	}, cancelWait
}

// Start boots the HTTP/WS server.
func Start(ctx context.Context, addr string, opts StartOptions) error {
	agentConfig, err := agent.LoadConfigWithExtra(opts.AgentConfigPath)
	if err != nil {
		return err
	}
	executable, _ := os.Executable()
	updateSvc := update.NewService(deploy.ReleaseRepo, opts.Version, executable, opts.Args, 10*time.Minute)
	updateSvc.Start(ctx)

	// 主页面登录/账户：只用于按账户分区，不参与 API 鉴权。
	authStore, err := auth.EnsureStore()
	if err != nil {
		log.Printf("[auth] init.error err=%v", err)
	}

	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return err
	}
	webPushConfig, err := webpush.EnsureConfig(opts.WebPushEnabled)
	if err != nil {
		log.Printf("[webpush] config.error err=%v", err)
	}

	// 共享设置与资源：建一次，所有账户共用同一份实例
	sharedPrefs, prefsErr := preferences.NewStore()
	if prefsErr != nil {
		log.Printf("[preferences] init.error err=%v", prefsErr)
	}
	sharedNodes, err := nodes.NewStore()
	if err != nil {
		log.Printf("[nodes] init.error err=%v", err)
	}
	sharedWebPush := webpush.NewService(webPushConfig, webpush.NewStoreAt(configDir))
	if err := sharedWebPush.SetRelayURL(opts.WebPushRelayURL); err != nil {
		// 配置写错不该让节点起不来：本机推送照常，只是 worker 的通知到不了浏览器。
		log.Printf("[webpush] relay.config.error err=%v", err)
	} else if sharedWebPush.RelayEnabled() {
		// 启动日志里能直接看出这台机器会不会转发通知 —— 排查「worker 不提醒」时，
		// 第一件事就是确认它有没有中继地址。
		log.Printf("[webpush] relay.enabled url=%s", strings.TrimSpace(opts.WebPushRelayURL))
	}
	sharedWorkflows, err := kanban.NewWorkflowStoreDefault()
	if err != nil {
		return err
	}
	// 控制面同步：默认关闭（opts.ControlSync == nil / Enabled=false）。
	// 开启后 store 建不出来、配置不完整一律启动失败 —— 半份副本比没有副本更危险。
	if opts.ControlSync != nil {
		opts.ControlSync.BuildVersion = opts.Version
	}
	controlSync, err := startControlSync(ctx, opts.ControlSync, nodeinfo.Normalize(opts.Role), configDir, controlSyncStores{
		auth:      authStore,
		prefs:     sharedPrefs,
		nodes:     sharedNodes,
		workflows: sharedWorkflows,
	})
	if err != nil {
		return err
	}
	// 边控端 relay：只有主节点侧（有 store、有 token）才会真的建起来。
	// 边控端自己不接受别人的隧道 —— 它拨出去的那条在 edgeside 装配。
	//
	// 变量用具体类型而不是接口：转发端（NewRelayHandler）与隧道端点要的能力面不同，
	// relayService 同时满足两者，取具体类型就不用在这里做一次转换。
	var controlSyncRelay *relayService
	if rs, err := startControlSyncRelay(controlSync, sharedNodes, opts.ControlSync); err != nil {
		return err
	} else if rs != nil {
		controlSyncRelay = rs
		// 现在挂进 wiring 而不是等 SetControlSync：每个账户的 HTTPHandler 是在
		// applyTo 里一次性拷贝这些字段的，wiring 之后被替换它就再也看不到 relay。
		controlSync.relay = rs
	}
	liveCompactDisableFile := opts.LiveCompactDisableFile
	liveCompactArchiveDir := opts.LiveCompactArchiveDir
	if liveCompactArchiveDir == "" {
		liveCompactArchiveDir = filepath.Join(configDir, "context")
	}
	if liveCompactDisableFile == "" {
		liveCompactDisableFile = filepath.Join(liveCompactArchiveDir, "DISABLED")
	}
	sharedPool := agent.NewPoolWithOptions(agentConfig, agent.PoolOptions{
		LiveCompactMode:               opts.LiveCompactMode,
		LiveCompactKeepRounds:         opts.LiveCompactKeepRounds,
		LiveCompactKeepTailTokens:     opts.LiveCompactKeepTailTokens,
		LiveCompactMinBlockTokens:     opts.LiveCompactMinBlockTokens,
		LiveCompactMinBodyBytes:       opts.LiveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages:   opts.LiveCompactMaxDriftMessages,
		LiveCompactRailTokens:         opts.LiveCompactRailTokens,
		LiveCompactMinFoldBytes:       opts.LiveCompactMinFoldBytes,
		LiveCompactMaxFoldBytes:       opts.LiveCompactMaxFoldBytes,
		LiveCompactLargeBlockTokens:   opts.LiveCompactLargeBlockTokens,
		LiveCompactDisableFile:        liveCompactDisableFile,
		LiveCompactArchiveDir:         liveCompactArchiveDir,
		LiveCompactPrefixGuard:        opts.LiveCompactPrefixGuard,
		LiveCompactPrefixUnsafeModels: opts.LiveCompactPrefixUnsafeModels,
		LiveCompactColdFold:           opts.LiveCompactColdFold,
		LiveCompactColdRailTokens:     opts.LiveCompactColdRailTokens,
		LiveCompactCacheTTLSeconds:    opts.LiveCompactCacheTTLSeconds,
		LiveCompactHarnesses:          opts.LiveCompactHarnesses,
	})
	sharedProber := agent.NewProber(&agentConfig, sharedPool, 5*time.Minute)
	sharedProber.Start(ctx)
	sharedPool.StartIdleReleaseLoop(ctx, func() time.Duration {
		hours := preferences.DefaultIdleSessionResourceReleaseHours
		if sharedPrefs != nil {
			hours = sharedPrefs.IdleSessionResourceReleaseHours()
		}
		return time.Duration(hours) * time.Hour
	})

	// 静默自动更新（D4）：只有安装包显式打开时才生效，且只在没有回合在跑时装。
	// 忙探针挂在共享 Pool 上 —— 与 repoint 掐进程用的是同一个判据（activeUses>0）。
	updateSvc.SetAutoInstall(opts.UpdateAutoInstall)
	updateSvc.SetBusyProbe(sharedPool.HasAnyActiveUse)
	if opts.UpdateAutoInstall {
		log.Printf("[update] auto_install.enabled — idle machines will install updates and restart")
	}

	// 外部通道先于工作区创建：每账户的 Kanban 实例在构建时就要拿到适配器，
	// 否则启动期建好的主账户永远发不出卡片。
	feishuChannel := newFeishuChannel(opts.Feishu, configDir)

	workspaces := newWorkspaceManager(ctx, sharedServices{
		agentConfig:      agentConfig,
		update:           updateSvc,
		auth:             authStore,
		notify:           notifyscript.NewService(notifyscript.Config{Script: opts.NotifyScript}),
		prefs:            sharedPrefs,
		nodes:            sharedNodes,
		webPush:          sharedWebPush,
		workflows:        sharedWorkflows,
		pool:             sharedPool,
		prober:           sharedProber,
		writerGate:       kanban.NewWriterGate(),
		routerConfig:     routerConfigFromOptions(opts),
		taskConfig:       taskConfigFromOptions(opts),
		hygiene:          sessionHygieneConfigFromOptions(opts.SessionHygiene),
		parallelMainTree: &opts.WorkflowParallelMainTree,
		feishu:           feishuChannel,
		feishuRelay:      feishuRelayConfigFromOptions(opts),
	})
	workspaces.SetBaseDir(filepath.Join(configDir, "users"))
	if feishuChannel != nil {
		if err := prepareFeishuChannel(feishuChannel, opts.Feishu, FeishuIntakeDeps{
			ConfigDir: configDir,
			NodeID:    opts.FeishuNodeID,
			Nodes:     sharedNodes,
			Workspace: workspaces,
		}); err != nil {
			// 入站装配失败不能杀掉进程：M1 的卡片出口与首页仍照常可用。
			log.Printf("[feishu] 入站装配失败，M2 不启用: %v", err)
		}
		feishuChannel.SetAnswerHandler(newFeishuAnswerRelay(workspaces))
		feishuChannel.Start(ctx)
	}

	// 主账户先建一次：启动期就把配置问题暴露出来，而不是等第一个请求 500
	primary, err := workspaces.Workspace("")
	if err != nil {
		return err
	}
	// 上游的 kanban.Start(ctx) 走并发调度器，本地已删（AGENTS.md 事实 13：无并发调度），
	// 看板实例由每账户 AppContext 惰性构建（app/workspace.go）。
	localCLIToken, err := EnsureLocalCLIToken(addr, opts.UseTLS)
	if err != nil {
		return err
	}
	// 运行节点不注入静态目录：serveStaticAsset 首行 staticDir == "" 的守卫
	// 就此关闭前端服务，不需要额外的开关。
	staticDir := ""
	if nodeinfo.Normalize(opts.Role) != nodeinfo.RoleWorker {
		staticDir = resolveStaticDir()
	}
	workspaces.SetRole(nodeinfo.Normalize(opts.Role))
	workspaces.SetHandlerDefaults(staticDir, func() string { return localCLIToken })
	workspaces.SetControlSync(controlSync)
	// 前端版本在运行期可变：边缘镜像到新构建后立刻出那一版，不需要重启进程
	// （用户的要求是「在线无感更新」，重启才生效就不叫无感了）。
	applyUIMirrorStaticDir(workspaces, controlSync)
	if controlSync != nil {
		// 后台循环：主节点兜底采集；边缘另有探活、追平、交接与推送。
		go controlSync.runtime.Run(ctx)
	}

	httpRoutes := api.NewScopedRouter(workspaces, func(appCtx *api.AppContext) http.Handler {
		return workspaces.NewHTTPHandler(appCtx, opts.Version).Routes()
	})
	wsRoutes := api.NewScopedRouter(workspaces, func(appCtx *api.AppContext) http.Handler {
		return &api.WSHandler{AppContext: appCtx}
	})

	mux := http.NewServeMux()
	inner := http.NewServeMux()
	// WithRawAccountPath 必须在 ScopedRouter 之前：账户段要在派发时就定下来。
	inner.Handle("/", api.WithRawAccountPath(httpRoutes))
	inner.Handle("/ws", wsRoutes)
	// 边控端 relay：/relay/<edge-name>/... 在这里被接住并转发进隧道。
	//
	// 必须是嵌在后端的一条真实路由，不能交给 nginx：后端在部署前缀之后接管了
	// 整个 origin（下面那行 mux.Handle("/", ...)），nginx 无法在同一个 origin
	// 下把 /mindfs/relay/* 切给另一个上游而不破坏其余路径。
	if controlSyncRelay != nil {
		inner.Handle("/relay/", api.NewRelayHandler(controlSyncRelay))
	}
	var root http.Handler = inner
	if controlSync != nil {
		root = controlSync.wrapRoot(inner)
	}
	mux.Handle("/", api.StripDeployPrefix(deploy.NormalizedPrefix(), root))

	handler := api.LoggingMiddleware(mux)

	// 边控端 relay 隧道：主动拨向主控，拨通后整台机器的 HTTP 面就挂在
	// 主控的 /relay/<name>/ 下面，别的设备不需要能直连这台机器。
	//
	// 交给隧道的是**剥掉部署前缀之后**的 root：主控转发进来时已经把
	// /mindfs/relay/<name> 换成了 /<rest>，再套一层 StripDeployPrefix 只会 404。
	// 它必须经过 wrapRoot —— 边缘在线时控制面请求照旧转发给主控，数据面
	// （会话/文件/agent）才落在本机，和本机浏览器访问走的是同一条路。
	//
	// 失败只记日志不返回错误：这台机器本地仍然可用，只是别的设备暂时连不上它。
	if _, err := startEdgeRelayTunnel(ctx, controlSync, api.LoggingMiddleware(root), opts.ControlSync); err != nil {
		log.Printf("[relay] tunnel client disabled: %v", err)
	}

	server, cancelWait := newWaitAwareServer(addr, handler)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		if primary.Prober != nil {
			primary.Prober.Stop()
		}
		if primary.Agents != nil {
			primary.Agents.CloseAll()
		}
		// 先取消请求上下文：长等待端点立刻返回，Shutdown 不必等它们超时。
		cancelWait()
		server.Shutdown(context.Background())
	}()

	if err := initializeCodexProfileSelection(sharedPrefs); err != nil {
		log.Printf("[codexsync] active_profile.init.error err=%v", err)
	}
	if opts.UseTLS {
		codexsync.StartRunner(ctx, 5*time.Minute)
		return server.ServeTLS(listener, opts.CertFile, opts.KeyFile)
	}
	codexsync.StartRunner(ctx, 5*time.Minute)
	return server.Serve(listener)
}
