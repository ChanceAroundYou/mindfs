package app

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/api"
	"mindfs/server/internal/config"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/scheduled"
)


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

	// 置顶表**每账户一份**（与偏好的共享相反）：置顶是「这个账户顶哪些会话/项目」，
	// 跨账户共享会让你的置顶出现在别人的会话栏里。
	//
	// 落在 configDir —— 那是本账户的目录，主账户就是 <config-dir>/、其余账户是
	// <config-dir>/users/<id>/。不用账户 id 当文件名：账户表每台机器各自生成
	// id，同一个账户在两台机器上会落到不同路径，那正是这次要消灭的漂移。
	pinStore, err := pins.NewStoreAt(configDir)
	if err != nil {
		// 读不了置顶表不该让整个账户打不开：降级成空表，用户重新点一次置顶就有了。
		// （NewStoreAt 内部已把「文件坏掉」降级成空表，这里只兜住路径类错误。）
		log.Printf("[pins] init.error account=%s err=%v", userID, err)
		pinStore = nil
	} else if pinStore.NeedsLegacySeed() {
		// 一次性回填：项目置顶原本住在**共享**偏好里，那是它跨账户泄漏的地方。
		// 只在本账户还没有置顶文件时搬一次，搬完就由该文件自己说话 ——
		// 否则用户在这里取消掉的置顶，会被共享偏好里的旧值在下次启动时搬回来。
		//
		// 主账户回填的是它自己那一份历史值；其余账户搬的是同一份共享值，
		// 也就是它们此前本来就能看到的那份 —— 不扩大可见范围，只是把它落成
		// 各账户自己的文件，从此互不影响。
		if err := pinStore.SeedProjects(prefs.SessionProjectPins()); err != nil {
			log.Printf("[pins] seed.error account=%s err=%v", userID, err)
		}
	}

	services := &api.AppContext{
		Dirs:       registry,
		Prefs:      prefs,
		Pins:       pinStore,
		Nodes:      m.shared.nodes,
		Agents:     m.shared.pool,
		Prober:     m.shared.prober,
		AccountDir: configDir,
		Update:     m.shared.update,
		Auth:       m.shared.auth,
		Notify:     m.shared.notify,
		WebPush:    m.shared.webPush,
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
	services.WireFinishStageTeardown(services.Kanban)
	services.WireSessionRunningProbe(services.Kanban)
	githubImportSvc, err := githubimport.NewService(services)
	if err != nil {
		return nil, err
	}
	services.GitHub = githubImportSvc
	for _, root := range services.ListRoots() {
		// 启动时把处于等待用户以外的旧待跑任务补进来跑一次（无调度器语义，仅确保不卡住）。
		services.Kanban.KickPending(root.ID)
	}

	log.Printf("[workspace] 已就绪 user=%s primary=%v projects=%s meta=%s",
		userID, primary, filepath.Dir(registryPath), metaRoot)
	return services, nil
}
