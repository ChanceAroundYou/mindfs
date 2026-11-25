package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes constructs the chi router with all endpoints.
// The deployment prefix is stripped once at the top-level mux (StripDeployPrefix),
// so every route here is registered prefix-free.
func (h *HTTPHandler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.corsMiddleware)
	r.Use(h.rejectControlPlaneOnWorker)
	r.Options("/*", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r.NotFound(h.handleNotFound)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { h.handleNotFound(w, r) })
	r.Get("/", h.handleFrontend)
	r.Get("/health", h.handleHealth)
	// 角色自述：前端靠它知道对面提不提供 UI（static=false 就不给「打开网页」入口）。
	// 它自己也在控制面前缀表里，所以 worker 上这个端点是 403 —— 前端据此
	// 「探测失败 = 没有 UI」，与「节点挂了」区分开。
	r.Get("/api/node-info", h.handleNodeInfo)
	// 主页面登录 + 账户管理：公开端点。
	r.Get("/api/auth/status", h.handleAuthStatus)
	r.Post("/api/auth/login", h.handleAuthLogin)
	r.Get("/api/users", h.handleUsersList)
	r.Post("/api/users", h.handleUserCreate)
	r.Put("/api/users/{id}", h.handleUserUpdate)
	r.Post("/api/users/{id}/primary", h.handleUserSetPrimary)
	r.Delete("/api/users/{id}", h.handleUserDelete)
	r.Get("/api/tree", h.handleTree)
	r.Get("/api/file", h.handleFile)
	// 原始资源（路径式）：网页预览的相对资源要靠路径才能解析到同目录文件。
	// 账户段由 WithRawAccountPath 中间件从路径里搬进 user=。
	r.Get("/api/file/raw/{root}/*", h.handleRawAsset)
	r.Put("/api/file", h.handleFileSave)
	r.Post("/api/file", h.handleFileCreate)
	r.Post("/api/file/operation", h.handleFileOperation)
	r.Get("/api/git/status", h.handleGitStatus)
	r.Get("/api/git/diff", h.handleGitDiff)
	r.Get("/api/git/history", h.handleGitHistory)
	r.Get("/api/git/commit/files", h.handleGitCommitFiles)
	r.Get("/api/git/commit/diff", h.handleGitCommitDiff)
	r.Get("/api/git/related-file/diff", h.handleGitRelatedFileDiff)
	// 批量版：一次拿回 N 个关联文件的 status/additions/deletions，不取 diff 正文。
	// 用 POST 是因为 80 个路径塞不进 URL；单文件那条 GET 仍然保留（点开看 diff 要正文）。
	r.Post("/api/git/related-files/stats", h.handleGitRelatedFileStats)
	r.Get("/api/git/branches", h.handleGitBranches)
	r.Get("/api/git/worktrees", h.handleGitWorktreeList)
	r.Post("/api/git/checkout", h.handleGitCheckout)
	r.Post("/api/git/pull", h.handleGitPull)
	r.Post("/api/git/push", h.handleGitPush)
	r.Post("/api/git/commit", h.handleGitCommit)
	r.Post("/api/git/stage", h.handleGitStage)
	r.Post("/api/git/unstage", h.handleGitUnstage)
	r.Post("/api/git/discard", h.handleGitDiscard)
	r.Post("/api/git/worktrees", h.handleGitWorktreeCreate)
	r.Delete("/api/git/worktrees", h.handleGitWorktreeRemove)
	r.Post("/api/upload", h.handleUpload)
	r.Get("/api/candidates", h.handleCandidates)
	r.Post("/api/prompts", h.handlePromptSave)
	r.Delete("/api/prompts", h.handlePromptDelete)
	r.Get("/api/sessions", h.handleSessions)
	r.Get("/api/preferences/session-naming", h.handleSessionNamingPreferenceGet)
	r.Put("/api/preferences/session-naming", h.handleSessionNamingPreferencePut)
	r.Get("/api/preferences/idle-session-resource-release", h.handleIdleSessionResourceReleasePreferenceGet)
	r.Put("/api/preferences/idle-session-resource-release", h.handleIdleSessionResourceReleasePreferencePut)
	r.Get("/api/agents/memory", h.handleAgentMemoryGet)
	r.Post("/api/agents/release-idle", h.handleAgentIdleRelease)
	r.Get("/api/preferences/new-project-meta-location", h.handleNewProjectMetaLocationPreferenceGet)
	r.Put("/api/preferences/new-project-meta-location", h.handleNewProjectMetaLocationPreferencePut)
	r.Get("/api/preferences/cors", h.handleCORSPreferenceGet)
	r.Put("/api/preferences/cors", h.handleCORSPreferencePut)
	// 置顶是控制面：权威在主节点，worker 上 403（nodeinfo 前缀表含 /api/pins）。
	r.Get("/api/pins", h.handlePinsGet)
	r.Put("/api/pins/project", h.handlePinsProjectPut)
	r.Put("/api/pins/session", h.handlePinsSessionPut)
	r.Get("/api/replying-sessions", h.handleReplyingSessions)
	// 长轮询版本（带 If-None-Match 才等待）。与普通端点同属数据面，不进控制面前缀表。
	r.Get("/api/replying-sessions/wait", h.handleReplyingSessionsWait)
	r.Get("/api/sessions/search", h.handleSessionSearch)
	r.Get("/api/sessions/children", h.handleSessionChildren)
	r.Get("/api/sessions/external", h.handleExternalSessionsList)
	r.Post("/api/sessions/import", h.handleExternalSessionImport)
	r.Post("/api/sessions/import/batch", h.handleExternalSessionImportBatch)
	r.Post("/api/sessions/fork", h.handleSessionFork)
	r.Post("/api/sessions/{key}/repoint", h.handleSessionRepoint)
	// 无 path 参数的同一操作：给 wt-finish 这类只有 CLAUDE_CODE_SESSION_ID 的脚本用，
	// body 里带 agent_session_id，服务端反查 mindfs 会话。
	r.Post("/api/sessions/repoint", h.handleSessionRepoint)
	// 只摘 related_worktree，不 fork 转录。专治「归属目录已删、运行时 cwd 早已离开」
	// 的那批会话 —— 在它们身上 repoint 会在任何副作用之前 400，于是没有任何修复入口。
	r.Post("/api/sessions/{key}/worktree-ownership", h.handleSessionWorktreeOwnershipClear)
	r.Post("/api/sessions/worktree-ownership", h.handleSessionWorktreeOwnershipClear)
	r.Get("/api/sessions/runtime-issues", h.handleSessionRuntimeIssues)
	r.Post("/api/sessions/runtime-issues/sweep", h.handleSessionRuntimeIssuesSweep)
	r.Get("/api/sessions/maintenance/diagnose", h.handleSessionMaintenanceDiagnose)
	r.Post("/api/sessions/maintenance/locks/clean", h.handleSessionMaintenanceCleanLocks)
	r.Post("/api/sessions/{key}/maintenance/images/clean", h.handleSessionMaintenanceCleanImages)
	r.Post("/api/sessions/{key}/maintenance/purge", h.handleSessionMaintenancePurgeEmpty)
	r.Get("/api/sessions/{key}/toolcalls/{callID}", h.handleSessionToolCallGet)
	r.Get("/api/sessions/{key}/context-saving", h.handleSessionContextSaving)
	r.Post("/api/sessions/{key}/compact", h.handleSessionCompact)
	r.Post("/api/sessions/{key}/sync", h.handleSessionSync)
	r.Get("/api/sessions/{key}", h.handleSessionGet)
	r.Post("/api/sessions/{key}/messages", h.handleSessionUserMessage)
	r.Get("/api/sessions/{key}/related-files", h.handleSessionRelatedFilesGet)
	r.Post("/api/sessions/{key}/pin", h.handleSessionPin)
	r.Post("/api/sessions/{key}/archive", h.handleSessionArchive)
	r.Post("/api/sessions/{key}/rename", h.handleSessionRename)
	r.Delete("/api/sessions/{key}/related-files", h.handleSessionRelatedFilesDelete)
	r.Delete("/api/sessions/{key}", h.handleSessionDelete)
	r.Get("/api/scheduled-agent-tasks", h.handleScheduledAgentTasksList)
	r.Post("/api/scheduled-agent-tasks", h.handleScheduledAgentTaskCreate)
	r.Put("/api/scheduled-agent-tasks/{id}", h.handleScheduledAgentTaskUpdate)
	r.Delete("/api/scheduled-agent-tasks/{id}", h.handleScheduledAgentTaskDelete)
	r.Post("/api/scheduled-agent-tasks/{id}/run", h.handleScheduledAgentTaskRun)
	r.Get("/api/workflows", h.handleWorkflowsList)
	r.Get("/api/workflows/published", h.handleWorkflowVersionsList)
	r.Get("/api/workflows/available", h.handleWorkflowsAvailable)
	r.Post("/api/workflows/recommend", h.handleWorkflowRecommend)
	r.Post("/api/workflows", h.handleWorkflowDraftCreate)
	r.Post("/api/workflows/authoring/proposals", h.handleWorkflowCreateAuthoringProposal)
	r.Post("/api/workflows/authoring/validate", h.handleWorkflowCreateAuthoringValidate)
	r.Post("/api/workflows/authoring/apply", h.handleWorkflowCreateAuthoringApply)
	r.Post("/api/workflows/dry-run", h.handleWorkflowDryRun)
	r.Post("/api/workflows/publish-proposals/{proposalID}/confirm", h.handleWorkflowPublishProposalConfirm)
	r.Get("/api/workflows/{id}/snapshot", h.handleWorkflowDraftSnapshot)
	r.Post("/api/workflows/{id}/patch/validate", h.handleWorkflowDraftPatchValidate)
	r.Post("/api/workflows/{id}/patch/apply", h.handleWorkflowDraftPatchApply)
	r.Post("/api/workflows/{id}/authoring/proposals", h.handleWorkflowDraftAuthoringProposal)
	r.Post("/api/workflows/{id}/publish-proposals", h.handleWorkflowPublishProposal)
	r.Get("/api/workflows/{id}/publish-proposals", h.handleWorkflowPublishProposalList)
	r.Put("/api/workflows/{id}", h.handleWorkflowDraftUpdate)
	r.Put("/api/workflows/{id}/meta", h.handleWorkflowMetaUpdate)
	r.Delete("/api/workflows/{id}", h.handleWorkflowDraftDelete)
	r.Post("/api/workflows/{id}/publish", h.handleWorkflowDraftPublish)
	r.Get("/api/workflows/versions/{version}", h.handleWorkflowVersionGet)
	r.Get("/api/tasks/{id}/workflow-run", h.handleTaskWorkflowRunGet)
	r.Put("/api/tasks/{id}/workflow-run", h.handleTaskWorkflowRunEdit)
	r.Get("/api/tasks/{id}/workflow-run/nodes", h.handleTaskWorkflowNodeRunsGet)
	r.Post("/api/tasks/{id}/workflow-run/patch/validate", h.handleTaskWorkflowRunPatchValidate)
	r.Post("/api/tasks/{id}/workflow-run/patch/apply", h.handleTaskWorkflowRunPatchApply)
	r.Post("/api/tasks/{id}/workflow-run/authoring/proposals", h.handleTaskWorkflowRunAuthoringProposal)
	r.Post("/api/tasks/{id}/workflow-run/dry-run", h.handleTaskWorkflowRunDryRun)
	r.Get("/api/tasks/{id}/workflow-run/suggestion", h.handleTaskWorkflowRunSuggestion)
	r.Post("/api/tasks/{id}/workflow-run/nodes/{nodeRunID}/retry", h.handleTaskWorkflowNodeRunRetry)
	r.Post("/api/tasks/{id}/migrate-workflow", h.handleTaskMigrateToWorkflow)
	r.Get("/api/tasks/{id}/engine", h.handleTaskEngineGet)
	r.Get("/api/tasks/{id}/activities", h.handleTaskActivitiesList)
	r.Post("/api/tasks/{id}/activities", h.handleTaskActivitiesCreate)
	r.Get("/api/tasks/{id}/activities/{activityId}", h.handleTaskActivityGet)
	// Router 提问卡的作答入口：答案落成用户留言 + 卡片收口（会话卡不走这里）。
	r.Post("/api/tasks/{id}/activities/{activityId}/answer", h.handleTaskQuestionAnswer)
	// 完成证据清单（只读投影：报告条目 + 补做预算 + 复核结论）。
	r.Get("/api/tasks/{id}/completion", h.handleTaskCompletionGet)
	// Task Router 调度记录（只读；决策/会话是每节点自己的运行数据）。
	r.Get("/api/tasks/{id}/router-decisions", h.handleTaskRouterDecisions)
	r.Get("/api/tasks/{id}/router-decisions/{decisionId}", h.handleTaskRouterDecisionGet)
	r.Get("/api/tasks/{id}/router-session", h.handleTaskRouterSession)
	r.Get("/api/tasks/{id}/router-session/transcript", h.handleTaskRouterSessionTranscript)
	r.Get("/api/router/status", h.handleRouterStatus)
	r.Get("/api/tasks", h.handleKanbanTasksList)
	r.Get("/api/tasks/overview", h.handleKanbanTasksOverview)
	r.Post("/api/tasks", h.handleKanbanTaskCreate)
	r.Post("/api/tasks/{id}/rename", h.handleKanbanTaskRename)
	r.Post("/api/tasks/{id}/run-now", h.handleKanbanTaskRunNow)
	r.Post("/api/tasks/{id}/start", h.handleKanbanTaskStart)
	r.Post("/api/tasks/{id}/terminate", h.handleKanbanTaskTerminate)
	r.Post("/api/tasks/{id}/pause", h.handleKanbanTaskPause)
	r.Post("/api/tasks/{id}/resume", h.handleKanbanTaskResume)
	r.Post("/api/tasks/{id}/complete", h.handleKanbanTaskComplete)
	r.Post("/api/tasks/{id}/cancel", h.handleKanbanTaskCancel)
	r.Post("/api/tasks/{id}/fail", h.handleKanbanTaskFail)
	r.Post("/api/tasks/{id}/archive", h.handleKanbanTaskArchive)
	r.Post("/api/tasks/{id}/reopen", h.handleKanbanTaskReopen)
	r.Delete("/api/tasks/{id}", h.handleKanbanTaskDelete)
	r.Post("/api/tasks/{id}/rebuild-worktree", h.handleKanbanTaskRebuildWorktree)
	r.Post("/api/tasks/{id}/finish-worktree", h.handleKanbanTaskFinishWorktree)
	r.Post("/api/tasks/{id}/begin-finish", h.handleKanbanTaskBeginFinish)
	r.Post("/api/tasks/{id}/human-gate", h.handleKanbanTaskHumanGate)
	r.Post("/api/tasks/{id}/workflow-publish", h.handleKanbanTaskWorkflowPublish)
	r.Get("/api/dirs", h.handleDirs)
	r.Post("/api/dirs", h.handleAddDir)
	r.Post("/api/dirs/{id}/rename", h.handleRenameDir)
	r.Post("/api/dirs/{id}/display-name", h.handleUpdateDirDisplayName)
	r.Delete("/api/dirs", h.handleRemoveDir)
	r.Get("/api/nodes", h.handleNodesList)
	r.Put("/api/nodes", h.handleNodesPut)
	r.Post("/api/nodes", h.handleNodesPost)
	r.Post("/api/nodes/validate-name", h.handleNodesValidateName)
	r.Delete("/api/nodes/{id}", h.handleNodesDelete)
	r.Get("/api/local_dirs", h.handleLocalDirs)
	r.Post("/api/local_dirs", h.handleLocalDirsCreate)
	r.Get("/api/app/update", h.handleAppUpdateGet)
	r.Post("/api/app/update", h.handleAppUpdatePost)
	// 控制面同步：只在启用时注册。nil 时保持改造前行为（两条路径 404），
	// 免得未配置的机器暴露一个永远报错的端点。
	if h.ControlSync != nil {
		r.Get("/api/control-sync/status", h.handleControlSyncStatus)
		r.Post("/api/control-sync/mode", h.handleControlSyncMode)
	}
	// 主节点的状态摘要：主节点没有 ModeController，但同步确实在跑，用户要能查。
	// 与边缘同一个地址，返回体带 role 区分；不含边控端名单与任何记录载荷，
	// 所以不挂 token —— 浏览器里的横幅要读它。
	if h.ControlSync == nil && h.ControlSyncPrimary != nil {
		r.Get("/api/control-sync/status", h.handleControlSyncStatus)
	}
	// 控制面同步：主节点侧的 /head /changes /push 一次能读出或改写整个控制面
	// （含口令 hash），所以只有配了 token 才注册，且一律走 bearer 校验。
	if h.ControlSyncToken != "" {
		if h.ControlSyncPush != nil {
			r.Post("/api/control-sync/push", h.requireSyncToken(h.handleControlSyncPush))
		}
		if h.ControlSyncFeed != nil {
			r.Get("/api/control-sync/head", h.requireSyncToken(h.handleControlSyncHead))
			r.Get("/api/control-sync/changes", h.requireSyncToken(h.handleControlSyncChanges))
		}
		// 前端资源镜像：边缘把主节点的构建产物搬到本机，之后主节点不可达也能出页面。
		// 与 /head /changes 同一档鉴权 —— 它同样能一次吐出整套静态资源。
		r.Get("/api/control-sync/ui/manifest", h.requireSyncToken(h.handleControlSyncUIManifest))
		r.Get("/api/control-sync/ui/bundle", h.requireSyncToken(h.handleControlSyncUIBundle))
		// 边控端名单：查看谁在同步、踢掉某台。鉴权必须与上面同档 ——
		// 能改名单就能让一台机器的同步停摆，那和能读控制面是同一个级别。
		if h.ControlSyncEdges != nil {
			r.Get("/api/control-sync/edges", h.requireSyncToken(h.handleControlSyncEdgesList))
			r.Post("/api/control-sync/edges/{id}/revoke", h.requireSyncToken(h.handleControlSyncEdgeRevoke))
			r.Post("/api/control-sync/edges/{id}/restore", h.requireSyncToken(h.handleControlSyncEdgeRestore))
		}
		// 边控端隧道：控制通道与数据通道都由边控端主动拨出。
		//
		// 不套 requireSyncToken —— 那层是按 body 验签的，而这两条是 GET，
		// 签名覆盖的是规范化查询串（见 verifyRelaySignature）。校验在 handler 里。
		if h.ControlSyncRelay != nil {
			r.Get("/api/control-sync/relay/tunnel", h.handleRelayTunnel)
			r.Get("/api/control-sync/relay/channel", h.handleRelayChannel)
		}
	}
	if h.ControlSyncConflicts != nil {
		r.Get("/api/control-sync/conflicts", h.handleControlSyncConflicts)
		r.Post("/api/control-sync/conflicts/{id}/resolve", h.handleControlSyncConflictResolve)
	}
	// 配对：只挂在还没配好的边控端上。这条路径不能进 requireSyncToken —— 它存在的
	// 理由就是「这台机器还没有 token」。守卫是本机回环，见 handleControlSyncPair。
	if h.ControlSyncPair != nil {
		r.Post("/api/control-sync/pair", h.handleControlSyncPair)
		// 只读探针：前端靠它区分「等着配对」与「这台机器根本不同步」。
		r.Get("/api/control-sync/pair", h.handleControlSyncPairState)
	}
	r.Post("/api/imports/github", h.handleGitHubImportStart)
	r.Get("/api/web-push/status", h.handleWebPushStatus)
	r.Post("/api/web-push/subscriptions", h.handleWebPushSubscriptionSave)
	r.Delete("/api/web-push/subscriptions", h.handleWebPushSubscriptionDelete)
	r.Post("/api/web-push/test", h.handleWebPushTest)
	// 页面在场心跳 / 关页上报：订阅表是设备本地的，所以这条与其它 web-push 端点同源。
	r.Post("/api/web-push/presence", h.handleWebPushPresence)
	// worker → 控制节点的通知中继。它必须留在控制面上（worker 上没有订阅表，
	// 中继到 worker 是空操作），所以不进 worker 的允许清单即可 —— 默认 worker
	// 只放行数据面，这条会被 403。
	r.Post("/api/web-push/relay", h.handleWebPushRelay)

	// 飞书卡片中继：只有主控代发（凭据与 lark-cli 只在主控）。worker 自己的
	// 入口/点击端点仍属数据面，只在 nodeinfo.controlPlanePrefixes 里登记这一条。
	r.Post("/api/channels/feishu/relay", h.handleFeishuRelay)
	// worker 就地处理主控转来的点击；这是数据面端点，worker 必须能提供。
	r.Post("/api/channels/feishu/answer", h.handleFeishuAnswer)

	// Agent status API
	r.Get("/api/agents", h.handleAgentsList)
	r.Get("/api/agents/custom-models", h.handleAgentCustomModelsGet)
	r.Put("/api/agents/custom-models", h.handleAgentCustomModelsPut)
	r.Post("/api/agents/restart", h.handleAgentRestart)
	r.Get("/api/agents/codex/rate-limits", h.handleCodexRateLimitsGet)
	r.Post("/api/agents/codex/rate-limit-reset", h.handleCodexRateLimitReset)
	r.Get("/api/agent-config/defaults", h.handleAgentConfigDefaults)
	r.Get("/api/agent-config/backups", h.handleAgentConfigBackupsList)
	r.Post("/api/agent-config/backups", h.handleAgentConfigBackupCreate)
	r.Delete("/api/agent-config/backups", h.handleAgentConfigBackupDelete)
	r.Post("/api/agent-config/switch", h.handleAgentConfigSwitch)
	r.Get("/api/agent-config/sync", h.handleAgentConfigSyncStatus)
	r.Post("/api/agent-config/sync", h.handleAgentConfigSyncRequest)
	r.Get("/api/agent-api-providers", h.handleAgentAPIProvidersList)
	r.Post("/api/agent-api-providers", h.handleAgentAPIProviderCreate)
	r.Post("/api/agent-api-providers/sync", h.handleAgentAPIProvidersSync)
	r.Post("/api/agent-api-providers/sync-all", h.handleAgentAPIProvidersSyncAll)
	r.Post("/api/agent-api-providers/test", h.handleAgentAPIProviderTest)
	r.Delete("/api/agent-api-providers", h.handleAgentAPIProviderDelete)
	r.Post("/api/agent-api-providers/switch", h.handleAgentAPIProviderSwitch)
	r.NotFound(h.handleNotFound)

	return r
}
