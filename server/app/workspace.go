package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/config"
	"mindfs/server/internal/e2ee"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/relay"
	"mindfs/server/internal/scheduled"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

// sharedServices 是进程级共享、不随账户变化的部分。
//
// 共享用**同一份实例**，不是"同一份文件两份实例"：对带调度/后台循环的服务，
// 后者会让同一个任务被跑两次。
//
// 按账户分的只有三样：项目列表（registry）、项目工作状态（meta：会话/任务/上传/文件批注）、
// 以及按本账户项目调度的看板与定时任务服务实例。
type sharedServices struct {
	agentConfig  agent.Config
	relayBaseURL string
	update       *update.Service
	auth         *auth.Store
	e2ee         *e2ee.Manager
	notify       *notifyscript.Service
	relay        *relay.Manager
	relayTips    *relay.TipsService

	// 共享设置与资源
	prefs     *preferences.Store
	nodes     *nodes.Store
	webPush   *webpush.Service
	templates *kanban.TemplateStore
	pool      *agent.Pool
	prober    *agent.Prober
}

type workspaceEntry struct {
	once  sync.Once
	value *api.AppContext
	err   error
}

// workspaceManager 按账户惰性构建并缓存 AppContext。
//
// **共享范围**（用户 2026-09-17 定）：只有「加载的项目」和「项目里的会话」按账户分，
// 其余（偏好/节点/订阅/提示词/agent 配置/agent 进程池/看板模板）全部共享同一份实例。
//
//   - 项目列表：主账户 <cfg>/registry.json；其余 <cfg>/users/<id>/registry.json
//   - 项目工作状态（meta：会话库/任务库/上传/文件批注）：
//     主账户沿用项目内 .mindfs/ 或 ~/.mindfs/<rootID>；其余 <cfg>/users/<id>/meta/<rootID>
//   - 其余一律指向共享实例，因此两个账户改偏好/加节点/装订阅是同一份
//
// 为什么不把 meta 也共享、只拆出会话：任务库共享后两个账户各有一个调度实例读同一份文件，
// 同一个定时任务会跑两次；而 scheduled 执行时要用 registry.GetSessionManager 跑本账户的会话，
// 共享实例说不清该用谁的会话。所以项目工作状态整块按账户分。
type workspaceManager struct {
	ctx       context.Context
	baseDir   string // <cfg>/users
	staticDir string

	shared sharedServices

	cliToken func() string

	mu      sync.Mutex
	entries map[string]*workspaceEntry
}

func newWorkspaceManager(ctx context.Context, shared sharedServices) *workspaceManager {
	return &workspaceManager{
		ctx:     ctx,
		shared:  shared,
		entries: map[string]*workspaceEntry{},
	}
}

// SetBaseDir 设定非主账户的数据根（<cfg>/users）。
func (m *workspaceManager) SetBaseDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baseDir = strings.TrimSpace(dir)
}

// SetHandlerDefaults 供 handler 构建时读取（静态资源目录、CLI token）。
func (m *workspaceManager) SetHandlerDefaults(staticDir string, cliToken func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.staticDir = staticDir
	m.cliToken = cliToken
}

func (m *workspaceManager) handlerDefaults() (string, func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.staticDir, m.cliToken
}

// PrimaryUserID 是存量数据的归属账户。
func (m *workspaceManager) PrimaryUserID() string {
	if m == nil || m.shared.auth == nil {
		return ""
	}
	return m.shared.auth.PrimaryUserID()
}

// Workspace 返回账户的工作区。userID 为空表示主账户。
func (m *workspaceManager) Workspace(userID string) (*api.AppContext, error) {
	if m == nil {
		return nil, errors.New("workspaces not configured")
	}
	if m.shared.auth == nil {
		return nil, errors.New("账户表未配置")
	}
	id := strings.TrimSpace(userID)
	if id == "" {
		id = m.shared.auth.PrimaryUserID()
	}
	if id == "" {
		return nil, errors.New("账户表为空")
	}
	if !m.shared.auth.Exists(id) {
		return nil, fmt.Errorf("%w: %s", api.ErrUnknownUser, id)
	}

	m.mu.Lock()
	entry, ok := m.entries[id]
	if !ok {
		entry = &workspaceEntry{}
		m.entries[id] = entry
	}
	m.mu.Unlock()

	entry.once.Do(func() {
		entry.value, entry.err = m.build(id)
	})
	return entry.value, entry.err
}

// NewHTTPHandler 为账户构建 HTTP handler（供 api.ScopedRouter 使用）。
func (m *workspaceManager) NewHTTPHandler(ctx *api.AppContext, version string) *api.HTTPHandler {
	staticDir, cliToken := m.handlerDefaults()
	token := ""
	if cliToken != nil {
		token = cliToken()
	}
	return &api.HTTPHandler{
		AppContext:    ctx,
		StaticDir:     staticDir,
		Version:       version,
		LocalCLIToken: token,
	}
}

func (m *workspaceManager) build(userID string) (*api.AppContext, error) {
	primary := userID == m.PrimaryUserID()

	accountDir := ""
	metaRoot := ""
	if !primary {
		if strings.TrimSpace(m.baseDir) == "" {
			return nil, errors.New("账户数据根未配置")
		}
		accountDir = filepath.Join(m.baseDir, userID)
		metaRoot = filepath.Join(accountDir, "meta")
	}

	configDir := accountDir
	if configDir == "" {
		dir, err := config.MindFSConfigDir()
		if err != nil {
			return nil, err
		}
		configDir = dir
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}

	// 项目列表按账户分（各看各的项目），其余设置一律共享。
	// 共享用同一份实例而不是"同一份文件两份实例"——后者对调度类服务会双跑同一个任务。
	registryPath := filepath.Join(configDir, "registry.json")
	if accountDir != "" {
		registryPath = filepath.Join(accountDir, "registry.json")
	}
	registry := fs.NewRegistryAt(registryPath, metaRoot)
	if err := registry.Load(); err != nil {
		return nil, err
	}
	prefs := m.shared.prefs
	autoAddExternalProjectRoots(registry, prefs)
	startExternalProjectDiscoveryLoop(m.ctx, registry, prefs)

	services := &api.AppContext{
		Dirs:       registry,
		Prefs:      prefs,
		Nodes:      m.shared.nodes,
		Agents:     m.shared.pool,
		Prober:     m.shared.prober,
		AccountDir: configDir,
		Update:     m.shared.update,
		Auth:       m.shared.auth,
		E2EE:       m.shared.e2ee,
		Notify:     m.shared.notify,
		WebPush:    m.shared.webPush,
		Relay:      m.shared.relay,
		RelayTips:  m.shared.relayTips,
	}
	services.Scheduled = scheduled.NewService(services, services)
	if err := normalizeRegisteredForkSessions(m.ctx, services); err != nil {
		return nil, err
	}
	services.Scheduled.Start(m.ctx)

	// 看板与定时任务必须每账户一个实例：它们按「本账户的项目」调度，
	// 并在执行时用本账户的 session manager 跑（scheduled/tasks.go 用 registry.GetSessionManager）。
	// 共享实例会让同一个任务被两边各跑一次。
	services.Kanban = kanban.NewService(m.shared.templates, services)
	services.Kanban.SetRunner(services)
	githubImportSvc, err := githubimport.NewService(services)
	if err != nil {
		return nil, err
	}
	services.GitHub = githubImportSvc
	for _, root := range services.ListRoots() {
		services.Kanban.Schedule(root.ID)
	}

	log.Printf("[workspace] 已就绪 user=%s primary=%v projects=%s meta=%s",
		userID, primary, filepath.Dir(registryPath), metaRoot)
	return services, nil
}
