package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/internal/deploy"
	"mindfs/server/internal/agent"
	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/config"
	"mindfs/server/internal/e2ee"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/relay"
	"mindfs/server/internal/tlsutil"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

const staticDirEnvKey = "MINDFS_STATIC_DIR"
const externalProjectDiscoveryInterval = time.Minute
const hostedAgentsRefreshInterval = 10 * time.Minute

type StartOptions struct {
	NoRelayer       bool
	RelayBaseURL    string
	Version         string
	Args            []string
	AgentConfigPath string
	E2EEConfig      E2EEConfig
	WebPushEnabled  bool
	NotifyScript    string
	UseTLS          bool
	CertFile        string
	KeyFile         string
}

type E2EEConfig struct {
	Enabled       bool
	NodeID        string
	PairingSecret string
}

type E2EEEnsureResult struct {
	Config    E2EEConfig
	Generated bool
}

func EnsureE2EEConfig(enabled bool) (E2EEEnsureResult, error) {
	result, err := e2ee.EnsureConfig(enabled)
	if err != nil {
		return E2EEEnsureResult{}, err
	}
	return E2EEEnsureResult{
		Config: E2EEConfig{
			Enabled:       result.Config.Enabled,
			NodeID:        result.Config.NodeID,
			PairingSecret: result.Config.PairingSecret,
		},
		Generated: result.Generated,
	}, nil
}

// Start boots the HTTP/WS server.
func Start(ctx context.Context, addr string, opts StartOptions) error {
	agentConfig, err := agent.LoadConfigWithExtra(opts.AgentConfigPath)
	if err != nil {
		return err
	}
	relayBaseURL := opts.RelayBaseURL
	if relayBaseURL == "" {
		relayBaseURL = agentConfig.RelayBaseURL
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

	// relay 是进程级的（一台机器一条隧道），先建好再交给各账户共享
	relayMgr, err := relay.NewManager(addr, opts.NoRelayer, relayBaseURL, opts.UseTLS)
	if err != nil {
		return err
	}
	relayTips := relay.NewTipsService(relayMgr)

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
	startHostedAgentConfigLoop(ctx, relayBaseURL, agentConfig, sharedPool, sharedProber)
	sharedPool.StartIdleReleaseLoop(ctx, func() time.Duration {
		hours := preferences.DefaultIdleSessionResourceReleaseHours
		if sharedPrefs != nil {
			hours = sharedPrefs.IdleSessionResourceReleaseHours()
		}
		return time.Duration(hours) * time.Hour
	})

	workspaces := newWorkspaceManager(ctx, sharedServices{
		agentConfig:  agentConfig,
		relayBaseURL: relayBaseURL,
		update:       updateSvc,
		auth:         authStore,
		e2ee: e2ee.NewManager(e2ee.Config{
			Enabled:       opts.E2EEConfig.Enabled,
			NodeID:        opts.E2EEConfig.NodeID,
			PairingSecret: opts.E2EEConfig.PairingSecret,
		}),
		notify:     notifyscript.NewService(notifyscript.Config{Script: opts.NotifyScript}),
		relay:      relayMgr,
		relayTips:  relayTips,
		prefs:      sharedPrefs,
		nodes:      sharedNodes,
		webPush:    sharedWebPush,
		templates:  sharedTemplates,
		pool:       sharedPool,
		prober:     sharedProber,
	})
	workspaces.SetBaseDir(filepath.Join(configDir, "users"))

	// 主账户先建一次：启动期就把配置问题暴露出来，而不是等第一个请求 500
	primary, err := workspaces.Workspace("")
	if err != nil {
		return err
	}

	localCLIToken, err := EnsureLocalCLIToken(addr)
	if err != nil {
		return err
	}
	workspaces.SetHandlerDefaults(resolveStaticDir(), func() string { return localCLIToken })

	httpRoutes := api.NewScopedRouter(workspaces, func(appCtx *api.AppContext) http.Handler {
		return workspaces.NewHTTPHandler(appCtx, opts.Version).Routes()
	})
	wsRoutes := api.NewScopedRouter(workspaces, func(appCtx *api.AppContext) http.Handler {
		return &api.WSHandler{AppContext: appCtx}
	})

	mux := http.NewServeMux()
	inner := http.NewServeMux()
	inner.Handle("/", httpRoutes)
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

	if err := relayMgr.Start(ctx); err != nil {
		return err
	}
	relayTips.Start(ctx)

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

	if workspaces.shared.e2ee != nil {
		workspaces.shared.e2ee.StartCleanup(ctx.Done())
	}

	if opts.UseTLS {
		return server.ServeTLS(listener, opts.CertFile, opts.KeyFile)
	}
	return server.Serve(listener)
}

func normalizeRegisteredForkSessions(ctx context.Context, services *api.AppContext) error {
	for _, root := range services.ListRoots() {
		manager, err := services.GetSessionManager(root.ID)
		if err != nil {
			return fmt.Errorf("initialize session store for root %s: %w", root.ID, err)
		}
		if _, err := manager.ListMetas(ctx); err != nil {
			return fmt.Errorf("normalize fork sessions for root %s: %w", root.ID, err)
		}
	}
	return nil
}

func startHostedAgentConfigLoop(ctx context.Context, relayBaseURL string, localConfig agent.Config, pool *agent.Pool, prober *agent.Prober) {
	endpoint, err := hostedAgentsURL(relayBaseURL)
	if err != nil {
		log.Printf("[agents/hosted] disabled invalid_relay_base_url=%q err=%v", relayBaseURL, err)
		return
	}
	go func() {
		refresh := func() {
			merged, err := fetchHostedAgentConfig(ctx, endpoint, localConfig)
			if err != nil {
				log.Printf("[agents/hosted] refresh.error url=%s err=%v", endpoint, err)
				return
			}
			effective := pool.UpdateConfig(merged)
			prober.UpdateConfig(ctx, &effective)
			log.Printf("[agents/hosted] refresh.ok url=%s agents=%d", endpoint, len(effective.Agents))
		}
		refresh()
		ticker := time.NewTicker(hostedAgentsRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
}

func hostedAgentsURL(relayBaseURL string) (string, error) {
	base := strings.TrimSpace(relayBaseURL)
	if base == "" {
		return "", fmt.Errorf("relay base url required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("relay base url must be absolute")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agents"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func fetchHostedAgentConfig(ctx context.Context, endpoint string, localConfig agent.Config) (agent.Config, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return agent.Config{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return agent.Config{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agent.Config{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return agent.Config{}, err
	}
	hosted, err := agent.DecodeConfig(body)
	if err != nil {
		return agent.Config{}, err
	}
	return agent.MergeHostedConfig(hosted, localConfig), nil
}

func autoAddExternalProjectRoots(registry *fs.Registry, prefs *preferences.Store) {
	if registry == nil {
		return
	}
	existing := make(map[string]struct{})
	for _, root := range registry.List() {
		normalized := agent.NormalizeComparablePath(root.RootPath)
		if normalized != "" {
			existing[normalized] = struct{}{}
		}
	}
	added := 0
	for _, projectPath := range agent.DiscoverExternalProjectPaths() {
		normalized := agent.NormalizeComparablePath(projectPath)
		if normalized == "" {
			continue
		}
		if _, ok := existing[normalized]; ok {
			continue
		}
		if hasMindFSMetadataDir(projectPath) {
			continue
		}
		if agent.IsTemporaryWorkDir(projectPath) {
			continue
		}
		gitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		isWorktree, err := gitview.IsInsideWorktree(gitCtx, projectPath)
		cancel()
		if err == nil && isWorktree {
			continue
		}
		location := fs.MetaLocationProject
		if prefs != nil {
			location = prefs.NewProjectMetaLocation()
		}
		rootID := filepath.Base(filepath.Clean(projectPath))
		pending := fs.NewRootInfo(rootID, rootID, projectPath)
		pending.MetaLocation = location
		if _, err := pending.EnsureMetaDir(); err != nil {
			log.Printf("[startup/projects] auto add metadata skipped path=%s err=%v", projectPath, err)
			continue
		}
		if _, err := registry.UpsertWithMetaLocation(projectPath, location); err != nil {
			log.Printf("[startup/projects] auto add skipped path=%s err=%v", projectPath, err)
			continue
		}
		existing[normalized] = struct{}{}
		added++
	}
	if added > 0 {
		log.Printf("[startup/projects] auto added external project roots count=%d", added)
	}
}

func hasMindFSMetadataDir(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(projectPath, ".mindfs"))
	return err == nil && info.IsDir()
}

func startExternalProjectDiscoveryLoop(ctx context.Context, registry *fs.Registry, prefs *preferences.Store) {
	if registry == nil {
		return
	}
	ticker := time.NewTicker(externalProjectDiscoveryInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				autoAddExternalProjectRoots(registry, prefs)
			}
		}
	}()
}

func resolveStaticDir() string {
	if hinted := strings.TrimSpace(os.Getenv(staticDirEnvKey)); hinted != "" {
		if info, err := os.Stat(hinted); err == nil && info.IsDir() {
			return hinted
		}
	}

	if exe, err := os.Executable(); err == nil {
		return resolveStaticDirFromExecutable(exe)
	}
	return ""
}

func resolveStaticDirFromExecutable(exe string) string {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return ""
	}
	exeDir := filepath.Dir(exe)
	// 本地构建布局为仓库根目录 mindfs + web/dist。
	candidate := filepath.Join(exeDir, "web", "dist")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	// 发布 zip 解压后，web 目录和可执行文件在同一层级。
	candidate = filepath.Join(exeDir, "web")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	// 安装布局为 <prefix>/bin/mindfs + <prefix>/share/mindfs/web。
	candidate = filepath.Join(filepath.Dir(exeDir), "share", "mindfs", "web")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	return ""
}

func isFrontendStaticDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, name := range []string{"index.html", "favicon.svg"} {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func RemoveManagedDirFromRegistry(path string) error {
	registry, err := fs.NewDefaultRegistry()
	if err != nil {
		return err
	}
	if err := registry.Load(); err != nil {
		return err
	}
	_, err = registry.Remove(path)
	return err
}

// EnsureTLSCert resolves TLS certificate and key file paths for the server.
// When certFlag or keyFlag are empty, a self-signed certificate is generated
// under os.UserConfigDir/mindfs/ and reused across restarts.
func EnsureTLSCert(certFlag, keyFlag string) (string, string, error) {
	return tlsutil.EnsureCert(certFlag, keyFlag)
}
