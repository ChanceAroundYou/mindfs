package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/api"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/config"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/scheduled"
)

// workflowNodeCanceller binds the root whose session manager must receive the
// cancel. NodeRun itself carries no root id, while CancelSessionTurn needs one.
type workflowNodeCanceller struct {
	rootID string
	svc    *api.AppContext
}

func (c workflowNodeCanceller) CancelWorkflowNode(ctx context.Context, nodeRun kanban.NodeRun) error {
	return c.svc.CancelWorkflowNode(ctx, c.rootID, nodeRun)
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

		TaskAgent: m.shared.taskConfig.Agent,
		TaskModel: m.shared.taskConfig.Model,
		Hygiene:   usecase.NewHygieneTracker(m.shared.hygiene),
	}
	services.Scheduled = scheduled.NewService(services, services)
	if err := normalizeRegisteredForkSessions(m.ctx, services); err != nil {
		return nil, err
	}
	services.Scheduled.Start(m.ctx)

	// 看板与定时任务必须每账户一个实例：它们按「本账户的项目」调度，
	// 并在执行时用本账户的 session manager 跑（scheduled/tasks.go 用 registry.GetSessionManager）。
	// 共享实例会让同一个任务被两边各跑一次。
	services.Kanban = kanban.NewService(services)
	// 外部通道（飞书）是进程级一份；本账户的看板把卡片交给它，账户身份由适配器固定。
	services.Feishu = m.shared.feishu
	if m.shared.feishu != nil {
		services.Kanban.SetCardNotifier(feishuCardNotifier{svc: m.shared.feishu, accountID: userID})
	}
	// 工作流库属共享控制面数据（主节点一份），每账户的 Kanban 实例共用同一份；
	// 不接的话 /api/workflows 一律 503，迁移入口也就无从谈起。
	services.Kanban.Workflows = m.shared.workflows
	// 对话式活动流事件走本账户的 WS hub（Task 7）：hub 每账户独立，跨账户不能泄漏。
	services.Kanban.SetActivityBroadcaster(api.NewTaskActivityBroadcaster(services))
	services.Kanban.SetRunner(services)
	// Workflow Agent 节点走独立端口（D2/D5），不得经 legacy StageRun 双写。
	services.Kanban.SetWorkflowNodeRunner(services)
	// 纯对话模式（workflow_choice=disabled）由 Router 直连会话执行。
	services.Kanban.SetDirectExecutor(services)
	services.Kanban.SetWorkflowCommentSender(services)
	// 所有账户共用同一把物理工作树 writer gate（D15）；每账户一把等于没锁。
	services.Kanban.SetWriterGate(m.shared.writerGate)
	services.Kanban.SetParallelMainTree(m.shared.parallelMainTreeEnabled())
	services.Kanban.SetRouterConfig(m.shared.routerConfig)
	// 自动选择 workflow 用与 Router 同一套 agent/model；model 由 config 指定
	// （生产用 ok 这类便宜模型）。未配置 agent 时返回 nil，保持旧回退。
	services.Kanban.SetWorkflowLLM(api.NewWorkflowLLMClient(services, m.shared.routerConfig.Agent, m.shared.routerConfig.Model))
	// 执行节点只写自然语言；结果转换（outcome/summary）由 Router 模型完成。
	services.SetWorkflowOutputLLM(api.NewWorkflowLLMClient(services, m.shared.routerConfig.Agent, m.shared.routerConfig.Model))
	// 结算结果压缩（task #87）：同一套便宜 agent/model，把节点原文压成一段短总结。
	// 未配置 agent 时 NewWorkflowLLMClient 返回 nil，压缩器缺席 → 保持原文。
	services.Kanban.SetSummaryCompressor(api.NewWorkflowSummaryCompressor(services, m.shared.routerConfig.Agent, m.shared.routerConfig.Model))
	// Router 的 agent 适配器就是本账户的 AppContext（跑隐藏 TypeRouter 会话）。
	services.Kanban.SetRouterAgent(services)
	services.Kanban.SetRouterBroadcaster(api.NewTaskRouterDecisionBroadcaster(services))
	services.WireFinishTeardown(services.Kanban)
	services.WireSessionRunningProbe(services.Kanban)
	services.WireFoldUsageReader(services.Kanban)
	// Router 读会话转录（router_read_session）：只读本任务会话的文本块。
	services.WireRouterTranscriptReader(services.Kanban)
	// 看板任务归档的会话级联只打标记、不删后代（#86，用户裁定 A）。
	services.Kanban.SetSessionArchiver(api.NewTaskSessionArchiver(services))
	for _, root := range services.ListRoots() {
		services.Kanban.SetWorkflowNodeCancellerForRoot(root.ID, workflowNodeCanceller{rootID: root.ID, svc: services})
		// WP-03 启动恢复：必须在 KickPending（异步起 RunTask）之前同步执行，
		// 否则 pending 的 ContinueRequest 会被执行体先消费掉。探针按 root 传入（每个 root
		// 有自己的 session manager）；不可确定时 RecoverOnStartup 标 unknown 并把任务置
		// waiting_user，随后 KickPending 会跳过它 —— 不自动重发。
		probe := services.SessionStateProbeForRoot(root.ID)
		if plan, err := services.Kanban.RecoverOnStartupForRoot(m.ctx, root.ID, probe); err != nil {
			// DB 坏/ctx 取消不阻塞启动：request 行仍在，下次启动再恢复。
			log.Printf("[kanban/recover] root=%s recover error=%v", root.ID, err)
		} else if plan.NeedsUser {
			log.Printf("[kanban/recover] root=%s needs-user unknown=%v", root.ID, plan.UnknownIDs)
		}
		// coordinate 模式：启动对账之后再构造 Router runtime，让事件循环立即
		// 接管 startup_reconcile 与结算事件；其它模式维持按需懒构造。
		if services.Kanban.RouterConfig().ModeForRoot(root.ID) == kanban.RouterModeCoordinate {
			services.Kanban.RouterServiceForRoot(root.ID)
		}
	}
	// 看板归档（#86）：启动即扫一次，之后每小时把超过 7 天的终态任务真正归档。
	// 低频是有意的 —— 归档不是实时语义，晚一小时没有可见影响；真写 archived_at
	// 才能让「取消归档 / 工作台过滤 / 分组」三处只有一个答案。
	services.Kanban.StartArchiveSweeper(m.ctx)
	githubImportSvc, err := githubimport.NewService(services)
	if err != nil {
		return nil, err
	}
	services.GitHub = githubImportSvc

	log.Printf("[workspace] 已就绪 user=%s primary=%v projects=%s meta=%s",
		userID, primary, filepath.Dir(registryPath), metaRoot)
	return services, nil
}
