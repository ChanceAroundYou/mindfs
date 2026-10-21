package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
}

// Start boots the HTTP/WS server.
func Start(ctx context.Context, addr string, opts StartOptions) error {
	agentConfig, err := agent.LoadConfigWithExtra(opts.AgentConfigPath)
	if err != nil {
		return err
	}
	executable, _ := os.Executable()
	updateSvc := update.NewService("a9gent/mindfs", opts.Version, executable, opts.Args, 10*time.Minute)
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
	sharedTemplates, err := kanban.NewTemplateStore()
	if err != nil {
		return err
	}
	sharedPool := agent.NewPool(agentConfig)
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
		agentConfig: agentConfig,
		update:      updateSvc,
		auth:        authStore,
		notify:      notifyscript.NewService(notifyscript.Config{Script: opts.NotifyScript}),
		prefs:       sharedPrefs,
		nodes:       sharedNodes,
		webPush:     sharedWebPush,
		templates:   sharedTemplates,
		pool:        sharedPool,
		prober:      sharedProber,
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

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
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
		server.Shutdown(context.Background())
	}()

	if opts.UseTLS {
		return server.ServeTLS(listener, opts.CertFile, opts.KeyFile)
	}
	return server.Serve(listener)
}
