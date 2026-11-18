package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
const externalProjectDiscoveryInterval = time.Minute

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
	// Task Router 是节点本地配置；cli/cmd 不能 import server/internal/kanban，
	// 所以入口只传字符串/时长，在 Start 里归一化成 kanban.RouterConfig。
	RouterMode        string
	RouterAgent       string
	RouterModel       string
	RouterEffort      string
	RouterTurnTimeout time.Duration
	RouterIdleClose   time.Duration
	RouterRoots       map[string]string
	// WorkflowParallelMainTree 控制没有独立 worktree 的任务是否共用主树并行写。
	// 入口 flag 默认 true；config.json 的 workflow.parallel_main_tree 可关闭。
	WorkflowParallelMainTree bool
	// LiveCompact 是节点本地的上下文活压缩配置。空值 = off。
	// 入口传字符串/整数，app 再转成 proxy.LiveCompactConfig，原因同 Router。
	LiveCompactMode             string
	LiveCompactKeepRounds       int
	LiveCompactMinBlockTokens   int
	LiveCompactMinBodyBytes     int
	LiveCompactMaxDriftMessages int
	// LiveCompactDisableFile 是急停文件；空值 = <config-dir>/context/DISABLED。
	LiveCompactDisableFile string
	// LiveCompactArchiveDir 是折叠存档根；空值 = <config-dir>/context。
	LiveCompactArchiveDir string
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
	}
	sharedWorkflows, err := kanban.NewWorkflowStoreDefault()
	if err != nil {
		return err
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
		LiveCompactMode:             opts.LiveCompactMode,
		LiveCompactKeepRounds:       opts.LiveCompactKeepRounds,
		LiveCompactMinBlockTokens:   opts.LiveCompactMinBlockTokens,
		LiveCompactMinBodyBytes:     opts.LiveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages: opts.LiveCompactMaxDriftMessages,
		LiveCompactDisableFile:      liveCompactDisableFile,
		LiveCompactArchiveDir:       liveCompactArchiveDir,
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
		parallelMainTree: &opts.WorkflowParallelMainTree,
	})
	workspaces.SetBaseDir(filepath.Join(configDir, "users"))

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
	mux.Handle("/", api.StripDeployPrefix(deploy.NormalizedPrefix(), inner))

	handler := api.LoggingMiddleware(mux)

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
