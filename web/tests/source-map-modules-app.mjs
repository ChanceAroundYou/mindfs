// source-map 的 MODULES 分片：`src/app/**` 与其余顶层目录（hooks / shared / i18n / 入口）。
//
// 分片只为满足 file-size.md 的硬线（500 行）—— 这张表有 152 条、555 行，
// 塞不进一个文件。**新增/修改条目照旧写在这里**，source-map.mjs 只负责合并导出。

export const APP_MODULES = {
  "src/main.tsx": [
    "src/main.tsx",
    "src/app/assetRecovery.ts",
    "src/app/useNativeShellLifecycle.ts",
    "src/app/nativeShellUtils.ts",
  ],
  "src/i18n/locales/en-US.ts": [
    "src/i18n/locales/en-US.ts",
    "src/i18n/locales/en-US-sessions.ts",
    "src/i18n/locales/en-US-tasks.ts",
    "src/i18n/locales/en-US-projects.ts",
    "src/i18n/locales/en-US-agents.ts",
    "src/i18n/locales/en-US-workspace.ts",
  ],
  "src/i18n/locales/zh-CN.ts": [
    "src/i18n/locales/zh-CN.ts",
    "src/i18n/locales/zh-CN-sessions.ts",
    "src/i18n/locales/zh-CN-tasks.ts",
    "src/i18n/locales/zh-CN-projects.ts",
    "src/i18n/locales/zh-CN-agents.ts",
    "src/i18n/locales/zh-CN-workspace.ts",
  ],
  "src/app/useSessionStreamCache.ts": [
    "src/app/useSessionStreamCache.ts",
    "src/app/useSessionStreamRuntimeMeta.ts",
    "src/app/sessionStreamCacheTypes.ts",
  ],
  "src/app/useAppStateLayer.ts": [
    "src/app/useAppStateLayer.ts",
    "src/app/useAppStateRefs.ts",
    "src/app/useAppRootSelection.ts",
    "src/app/useAppFileWorkspaceState.ts",
    "src/app/useAppSessionListState.ts",
    "src/app/useAppSessionSelectionState.ts",
    "src/app/useAppResponsiveState.ts",
    "src/app/useAppRootScopeState.ts",
    "src/app/useAppTaskTemplateState.ts",
  ],
  "src/app/useProjectLifecycle.tsx": [
    "src/app/useProjectLifecycle.tsx",
    "src/app/useProjectDirectoryBrowser.ts",
    "src/app/useProjectAddActions.ts",
    "src/components/file/WorktreeLifecycleOverlays.tsx",
  ],
  // useRealtimeEvents 拆分：34 个 WS 事件处理器（957 行）→ realtimeWsHandlers.ts。
  // 它们都关闭在 hook 的 state/ref/ctx 上，靠 createRealtimeWsHandlers(ctx, local)
  // 把 10 个局部函数当参数传进去；hook 侧只剩「派发」这一件事。
  "src/app/realtimeWsHandlers.ts": [
    "src/app/realtimeWsHandlers.ts",
    "src/app/realtimeWsTaskHandlers.ts",
  ],
  "src/app/useRealtimeEvents.ts": [
    "src/app/useRealtimeEvents.ts",
    "src/app/useRealtimeEventsContext.ts",
    "src/app/realtimeWsHandlers.ts",
    "src/app/realtimeWsTaskHandlers.ts",
    "src/app/realtimeWsFileChangedHandlers.ts",
    "src/app/realtimeWsSlashCommandHandlers.ts",
    "src/app/realtimeWsSessionStream.ts",
    "src/app/realtimeWsSessionMeta.ts",
    "src/app/realtimeWsUserMessage.ts",
    "src/app/realtimeWsSessionAccepted.ts",
    "src/app/realtimeWsRootChanged.ts",
  ],

  // App.tsx 拆分：会话缓存 → useSessionCache；发送消息 → useSendMessage；
  // 文件/目录动作 → useActionHandlers；会话生命周期 → useSessionLifecycle；
  // 多项目会话 → useMultiProjectSessions
  "src/App.tsx": [
    "src/App.tsx",
    "src/app/useSessionCache.ts",
    "src/app/useSendMessage.ts",
    "src/app/prepareSendMessageTarget.ts",
    "src/app/useSendMessageSlashCommand.ts",
    "src/app/useSendMessageTypes.ts",
    "src/app/useActionHandlers.ts",
    "src/app/openDirectoryAction.ts",
    "src/app/useSessionLifecycle.ts",
    "src/app/useSessionListLoading.ts",
    "src/app/useSessionSyncAction.ts",
    "src/app/useSessionSelection.ts",
    "src/app/useSessionLifecycleTypes.ts",
    "src/app/useMultiProjectSessions.ts",
    "src/app/useTaskWorkspaceState.ts",
    "src/app/useTaskWorkspaceEffects.ts",
    "src/app/useRootContentView.ts",
    "src/app/useProjectTreeActions.ts",
    "src/app/useUiPreferences.ts",
    "src/app/useSessionArchive.ts",
    "src/app/useManagedRoots.ts",
    "src/app/useManagedRootRename.ts",
    "src/app/useManagedRootsTypes.ts",
    "src/app/usePluginHandlers.ts",
    "src/app/useFileRefresh.ts",
    "src/app/useRelatedFileDiff.ts",
    "src/components/app/TaskSessionErrorDialog.tsx",
    "src/components/app/TaskDetailPanelGate.tsx",
    "src/components/app/OnboardingGate.tsx",
    "src/components/app/TaskInlineEditPanel.tsx",
    "src/components/app/TaskInlineCandidateMenu.tsx",
    "src/components/app/SessionSheet.tsx",
    "src/components/app/AppActionBar.tsx",
    "src/components/app/SessionPane.tsx",
    "src/components/app/SessionMainContent.tsx",
    "src/components/app/PendingPluginTrustView.tsx",
    "src/components/app/sessionPaneTypes.ts",
    "src/components/app/FileTreePanel.tsx",
    "src/app/renderRootSlashCommandResultHelpers.tsx",
    "src/app/usePopStateSync.ts",
    "src/app/useArchiveSync.ts",
    "src/app/useSessionRestore.ts",
    "src/app/useTaskInlineCandidates.ts",
    "src/app/usePluginThemeVars.ts",
    "src/app/useAppInit.ts",
    "src/app/useTaskBoard.ts",
    "src/app/useTaskComposer.ts",
    "src/app/useAttachedFileContext.ts",
    "src/app/useReplyingSessionPoll.ts",
    "src/app/useSessionPending.ts",
    "src/app/useSessionMetadata.ts",
    "src/app/useSessionListActions.ts",
    "src/app/useSessionRelatedFiles.ts",
    "src/app/useSessionQueueActions.ts",
    // App.tsx 深度拆分（2026-10-08）新抽出的域 hook —— 必须登记在这里，
    // 否则 50 个读 "src/App.tsx" 逻辑路径的结构测试会漏掉搬走的代码。
    "src/app/useAppProjectRemoval.ts",
    "src/app/useAppTaskDrawer.ts",
    "src/app/useAppAgentActions.ts",
    // 2026-10-08 第 1 步：置顶 / URL 状态 / 目录加载错误。
    "src/app/useAppSessionPins.ts",
    "src/app/useAppReplaceURLState.ts",
    "src/app/useAppFormatDirectoryLoadError.ts",
    // 2026-10-08 第 2 步：启动期三件事（自更新 / 引导步进 / 应用初始化）。
    "src/app/useAppBootstrapActions.ts",
    // 2026-10-08 第 3 步：会话列表的两种「再拉一次」+ 按复合键存的派生状态。
    "src/app/useAppSessionReload.ts",
    "src/app/useAppSessionKeyState.ts",
    // 2026-10-08 第 4 步：每根绑定/抽屉写入器 + 切项目时的锁复位。
    "src/app/useAppRootBindingSetters.ts",
    // 2026-10-08 第 5 步：会话选择的四个入口。
    "src/app/useAppSessionSelection.ts",
    // 2026-10-08 第 6 步：插件信任状态机 + 插件渲染与主题变量。
    "src/app/useAppPluginTrust.ts",
    "src/app/useAppPluginRender.ts",
    // 2026-10-08 第 7 步：项目显示名/节点色/下拉选项 + 路径点击与文件树派生视图。
    "src/app/useAppProjectDisplay.ts",
    "src/app/useAppFileNavigation.ts",
    // 2026-10-08 第 8 步：会话快照与查找表（含 CUSTOM(G-Z) 的 boardSessionByKey）。
    "src/app/useAppSessionSnapshots.ts",
    "src/app/useAppWorkspaceActions.ts",
    "src/app/useAppRootResolution.ts",
    // 2026-10-08 第 9-10 步
    "src/app/useAppBoundSessionDir.ts",
    "src/app/useAppMainViewActions.ts",
    // 2026-10-08 第 11 步
    "src/app/useAppSlashCommandResult.ts",
    "src/app/useAppKanbanStageColumns.ts",
    // 2026-10-08 第 12 步
    "src/app/useAppSessionView.tsx",
    "src/app/useAppRootContentRenderers.tsx",
    // 2026-10-08 第 13 步
    "src/app/useAppKanbanTaskPanel.tsx",
    // 2026-10-08 第 14 步：ref 镜像 + 同一段的 24 条 effect
    "src/app/useAppRefSync.ts",
    // 2026-10-08 第 14 步（续）：bootstrap 与插件渲染后的 effect
    "src/app/useAppBootstrapEffects.ts",
    "src/app/useAppPluginRenderEffects.ts",
    // 2026-10-08 分层聚合（App.tsx → 1000 以内）第 1 层：状态与 ref。
    // 这一层整体（原 L281-950，连续区间）搬走。hook 调用顺序里只有 1 个 useRef
    // （sessionListReloadTimerRef）从第 2 层上移至此层，总数 258 不变。
    "src/app/useAppStateLayer.ts",
    "src/app/useAppStateRefs.ts",
    "src/app/useAppRootSelection.ts",
    "src/app/useAppFileWorkspaceState.ts",
    "src/app/useAppSessionListState.ts",
    "src/app/useAppSessionSelectionState.ts",
    "src/app/useAppResponsiveState.ts",
    "src/app/useAppRootScopeState.ts",
    "src/app/useAppTaskTemplateState.ts",
    // 2026-10-08 分层聚合（App.tsx → 1000 以内）第 2 层：会话域。
    "src/app/useAppSessionLayer.ts",
    "src/app/useAppSessionLayerTypes.ts",
    "src/app/useAppSessionCore.ts",
    "src/app/useAppMultiProjectReload.ts",
    // 2026-10-08 分层聚合（App.tsx → 1000 以内）第 3 层：领域接线与实时通道。
    // 受管根、动作处理器、文件导航、项目生命周期、插件、实时事件与派生状态。
    "src/app/useAppDomainLayer.ts",
    "src/app/useAppDomainLayerTypes.ts",
    "src/app/useAppProjectManagement.ts",
    "src/app/useAppDomainDerived.ts",
    // 2026-10-08 分层聚合（App.tsx → 1000 以内）第 4 层：视图与渲染组合。
    // 这一层整体（L2276-2633，连续区间）搬走，hook 调用顺序与搬出前逐字一致。
    "src/app/useAppViewComposition.ts",
    "src/app/useAppViewCompositionTypes.ts",
    "src/app/useAppWorkspaceBoardComposition.ts",
    // 2026-10-08 分层聚合（App.tsx → 1000 以内）收尾：把 App() 的整段 JSX return
    // （原 L2635-2957，323 行）搬进 AppRootView。App.tsx 因此只剩四层调用 + 一次展开。
    "src/app/AppRootView.tsx",
  ],
  "src/hooks/useMarkdownViewer.tsx": [
    "src/hooks/useMarkdownViewer.tsx",
    "src/components/common/MarkdownImage.tsx",
  ],
  "src/shared/directorySort.ts": ["src/shared/directorySort.ts"],
};
