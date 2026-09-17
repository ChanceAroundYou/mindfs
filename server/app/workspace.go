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
	"time"

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
// 每个账户一套的（registry/prefs/nodes/agent 池/看板/订阅/定时）不在这里。
type sharedServices struct {
	agentConfig  agent.Config
	relayBaseURL string
	update       *update.Service
	auth         *auth.Store
	e2ee         *e2ee.Manager
	notify       *notifyscript.Service
	webPushCfg   webpush.Config
	relay        *relay.Manager
	relayTips    *relay.TipsService
}

type workspaceEntry struct {
	once  sync.Once
	value *api.AppContext
	err   error
}

// workspaceManager 按账户惰性构建并缓存 AppContext。
//
// 数据根的划分（见 docs/multi-user-prd.md §3.3）：
//   - 主账户：<cfg>/ + 项目内 .mindfs/，即迁移前的存量数据，行为零变化
//   - 其余账户：<cfg>/users/<id>/，meta 一律落 <cfg>/users/<id>/meta/<rootID>，
//     永不触碰项目里的 .mindfs，因此不可能与他人串号
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

	registry := fs.NewRegistryAt(filepath.Join(configDir, "registry.json"), metaRoot)
	if err := registry.Load(); err != nil {
		return nil, err
	}
	prefs, prefsErr := preferences.NewStoreAt(configDir)
	if prefsErr != nil {
		log.Printf("[preferences] init.error user=%s err=%v", userID, prefsErr)
	}
	autoAddExternalProjectRoots(registry, prefs)
	startExternalProjectDiscoveryLoop(m.ctx, registry, prefs)

	pool := agent.NewPool(m.shared.agentConfig)
	prober := agent.NewProber(&m.shared.agentConfig, pool, 5*time.Minute)
	prober.Start(m.ctx)
	startHostedAgentConfigLoop(m.ctx, m.shared.relayBaseURL, m.shared.agentConfig, pool, prober)
	pool.StartIdleReleaseLoop(m.ctx, func() time.Duration {
		hours := preferences.DefaultIdleSessionResourceReleaseHours
		if prefs != nil {
			hours = prefs.IdleSessionResourceReleaseHours()
		}
		return time.Duration(hours) * time.Hour
	})

	nodesStore, err := nodes.NewStoreAt(configDir)
	if err != nil {
		log.Printf("[nodes] init.error user=%s err=%v", userID, err)
	}
	webPushStore := webpush.NewStoreAt(configDir)

	services := &api.AppContext{
		Dirs:       registry,
		Prefs:      prefs,
		Nodes:      nodesStore,
		Agents:     pool,
		Prober:     prober,
		AccountDir: accountDir,
		Update:     m.shared.update,
		Auth:       m.shared.auth,
		E2EE:       m.shared.e2ee,
		Notify:     m.shared.notify,
		WebPush:    webpush.NewService(m.shared.webPushCfg, webPushStore),
		Relay:      m.shared.relay,
		RelayTips:  m.shared.relayTips,
	}
	services.Scheduled = scheduled.NewService(services, services)
	if err := normalizeRegisteredForkSessions(m.ctx, services); err != nil {
		return nil, err
	}
	services.Scheduled.Start(m.ctx)

	templates := kanban.NewTemplateStoreAt(configDir)
	services.Kanban = kanban.NewService(templates, services)
	services.Kanban.SetRunner(services)
	githubImportSvc, err := githubimport.NewService(services)
	if err != nil {
		return nil, err
	}
	services.GitHub = githubImportSvc
	for _, root := range services.ListRoots() {
		services.Kanban.Schedule(root.ID)
	}

	log.Printf("[workspace] 已就绪 user=%s primary=%v dir=%s", userID, primary, configDir)
	return services, nil
}
