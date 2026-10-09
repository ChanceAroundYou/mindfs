// 源码守卫测试的「逻辑模块 → 物理文件」单一映射。
//
// 背景：`web/tests/*.test.mjs` 里有大量测试直接读 `src/**` 的源码原文做正则断言
// （`assert.match(app, /…/)`）。这些断言按**逻辑模块**写，而不是按物理文件写；
// 一旦某个巨文件被拆分、或某个文件被按领域归类移动，物理路径就变了。
//
// 这里把「逻辑路径（测试里一直沿用的、相对 web/ 的路径）→ 当前物理文件列表」
// 收敛成唯一一处。文件被移动或拆分时只改 `MODULES`，测试与断言都不用动。
//
// 两个消费方：
//   · source-map-hook.mjs —— 预加载，patch `fs.readFileSync`，让所有既有读法
//     （`fs.readFileSync` / 具名 `readFileSync` / `read()` 本地 helper / `new URL`）
//     都自动走这张表；
//   · ts-module-hook.mjs —— 让 `await import("../src/…")` 也能解析到移动后的物理文件。
//
// 注意：本文件刻意用 `createRequire` 取 `node:fs`，而不是 ESM `import fs from "node:fs"`。
// 因为 ESM 具名导入是在 `node:fs` 的 ESM facade 建立时快照的，而 facade 一旦建立，
// 之后再 patch CJS 导出就覆盖不到具名导入了（实测）。createRequire 不建立 facade。
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const fs = require("node:fs");
// 捕获原始实现：readSource 自身必须绕过 patch，否则会递归。
const originalReadFileSync = fs.readFileSync.bind(fs);

/** 仓库 web/ 根目录。 */
export const WEB_ROOT = path.resolve(import.meta.dirname, "..");

/**
 * 逻辑路径（相对 web/）→ 物理文件（相对 web/，可多个）。
 *
 * 只登记「被移动过」或「被拆分过」的逻辑模块；未登记的一律按原路径读取。
 * 维护规则：文件移动/拆分时改这里，测试不动。
 */
export const MODULES = {
  // useRealtimeEvents 拆分：34 个 WS 事件处理器（957 行）→ realtimeWsHandlers.ts。
  // 它们都关闭在 hook 的 state/ref/ctx 上，靠 createRealtimeWsHandlers(ctx, local)
  // 把 10 个局部函数当参数传进去；hook 侧只剩「派发」这一件事。
  "src/app/useRealtimeEvents.ts": [
    "src/app/useRealtimeEvents.ts",
    "src/app/useRealtimeEventsContext.ts",
    "src/app/realtimeWsHandlers.ts",
    "src/app/realtimeWsSessionMeta.ts",
  ],

  // App.tsx 拆分：会话缓存 → useSessionCache；发送消息 → useSendMessage；
  // 文件/目录动作 → useActionHandlers；会话生命周期 → useSessionLifecycle；
  // 多项目会话 → useMultiProjectSessions
  "src/App.tsx": [
    "src/App.tsx",
    "src/app/useSessionCache.ts",
    "src/app/useSendMessage.ts",
    "src/app/useActionHandlers.ts",
    "src/app/useSessionLifecycle.ts",
    "src/app/useSessionLifecycleTypes.ts",
    "src/app/useMultiProjectSessions.ts",
    "src/app/useTaskWorkspaceState.ts",
    "src/app/useTaskWorkspaceEffects.ts",
    "src/app/useRootContentView.ts",
    "src/app/useProjectTreeActions.ts",
    "src/app/useUiPreferences.ts",
    "src/app/useSessionArchive.ts",
    "src/app/useManagedRoots.ts",
    "src/app/usePluginHandlers.ts",
    "src/app/useFileRefresh.ts",
    "src/app/useRelatedFileDiff.ts",
    "src/components/app/TaskSessionErrorDialog.tsx",
    "src/components/app/TaskDetailPanelGate.tsx",
    "src/components/app/E2eePrompt.tsx",
    "src/components/app/OnboardingGate.tsx",
    "src/components/app/TaskInlineEditPanel.tsx",
    "src/components/app/SessionSheet.tsx",
    "src/components/app/AppActionBar.tsx",
    "src/components/app/SessionPane.tsx",
    "src/components/app/FileTreePanel.tsx",
    "src/app/renderRootSlashCommandResult.tsx",
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
    // 2026-10-08 第 2 步：启动期四件事（自更新 / 引导步进 / E2EE 口令）。
    "src/app/useAppBootstrapActions.ts",
    // 2026-10-08 第 3 步：会话列表的两种「再拉一次」+ 按复合键存的派生状态。
    "src/app/useAppSessionReload.ts",
    "src/app/useAppSessionKeyState.ts",
    // 2026-10-08 第 4 步：每根绑定/抽屉写入器 + 切项目时的锁复位。
    "src/app/useAppRootBindingSetters.ts",
    // 2026-10-08 第 5 步：会话选择的四个入口。
    "src/app/useAppSessionSelection.ts",
  ],

  // P6 拆分：FileTree.tsx → + icons.tsx（图标子组件）
  "src/components/file/FileTree.tsx": [
    "src/components/file/FileTree.tsx",
    "src/components/file/FileTreeRefreshButton.tsx",
    "src/components/file/useAgentConfigFlowTransitions.ts",
    "src/components/file/fileTreeTypes.ts",
    "src/components/file/icons.tsx",
    "src/components/file/AgentConfigPopover.tsx",
    "src/app/usePwaInstall.ts",
    "src/components/file/fileTreeEntries.tsx",
    "src/components/file/useAgentConfigActions.ts",
    "src/components/file/useFileTreePreferences.ts",
    "src/components/file/useAgentConfigSelection.ts",
    "src/components/file/useAgentLifecycleActions.ts",
    "src/components/file/useAgentConfigOutsideClick.ts",
    "src/components/file/useFileTreeEffects.ts",
    "src/components/file/useAgentConfigSwitchRequest.ts",
    "src/components/file/FileTreeProjectTabs.tsx",
    "src/components/file/fileTreeMenuStyles.ts",
    "src/components/file/fileTreeMenuOptions.ts",
    "src/components/file/FileTreeEntryFilters.ts",
    "src/components/file/FileTreeFontSizeMenu.tsx",
    "src/components/file/FileTreeLocaleOptions.tsx",
    "src/components/file/FileTreeAppearanceOptions.tsx",
    "src/components/file/FileTreeSortOptions.tsx",
    "src/components/file/FileTreeFooter.tsx",
    "src/components/file/FileTreeProjectContent.tsx",
    "src/components/file/FileTreeIdleReleasePopover.tsx",
    "src/components/file/FileTreeSendShortcutPopover.tsx",
    "src/components/file/FileTreeSessionNamingPopover.tsx",
    "src/components/file/fileTreeAgentConfigUtils.ts",
  ],

  // P2 拆分：file.ts → services/file/（入口 shim 仍在原路径）
  "src/services/session.ts": ["src/services/session.ts"],
  "src/services/sessionCacheTypes.ts": ["src/services/sessionCacheTypes.ts"],
  "src/services/file.ts": [
    "src/services/file/types.ts",
    "src/services/file/cache.ts",
    "src/services/file/fetch.ts",
    "src/services/file/blob.ts",
    "src/services/file/index.ts",
  ],
  // P2 拆分：git.ts → services/git/（入口 shim 仍在原路径）
  "src/services/git.ts": [
    "src/services/git/types.ts",
    "src/services/git/status.ts",
    "src/services/git/history.ts",
    "src/services/git/actions.ts",
    "src/services/git/diff.ts",
    "src/services/git/relatedStats.ts",
    "src/services/git/index.ts",
  ],
  // P2 拆分：tasks.ts → services/task/（入口 shim 仍在原路径）
  "src/services/tasks.ts": [
    "src/services/task/types.ts",
    "src/services/task/cache.ts",
    "src/services/task/templates.ts",
    "src/services/task/crud.ts",
    "src/services/task/worktree.ts",
    "src/services/task/index.ts",
  ],
  "src/components/SessionList.tsx": [
    "src/components/session/SessionList.tsx",
    "src/components/session/sessionListTypes.ts",
    "src/components/session/sessionListShared.tsx",
    "src/components/session/SessionCard.tsx",
    "src/components/session/MultiProjectSessionList.tsx",
  ],
  "src/components/SessionViewer.tsx": [
    "src/components/session/SessionViewer.tsx",
    "src/components/session/useSessionWindowing.ts",
    "src/components/session/useTargetSeqScroll.ts",
    "src/components/session/sessionViewerTypes.ts",
    "src/components/session/sessionViewerFormat.ts",
    "src/components/session/SessionViewerAttachments.tsx",
    "src/components/session/SessionViewerTokenViews.tsx",
    "src/components/session/SessionViewerCards.tsx",
    "src/components/session/AskUserQuestionCard.tsx",
    "src/components/session/SessionViewerSlashCommand.tsx",
    "src/components/session/SessionViewerToolItem.tsx",
    "src/components/session/SessionViewerAuxiliaryTimeline.tsx",
    "src/components/session/SessionViewerThoughtItem.tsx",
    "src/components/session/SessionViewerUserMessage.tsx",
    "src/components/session/SessionViewerAssistantMessage.tsx",
    "src/components/session/SessionViewerRelatedFiles.tsx",
    "src/components/session/SessionViewerHeader.tsx",
    "src/components/session/SessionViewerLoadingState.tsx",
    "src/components/session/SessionViewerWaitingIndicator.tsx",
    "src/components/session/SessionViewerFloatingControls.tsx",
    "src/components/session/sessionViewerStyles.ts",
    "src/components/session/sessionViewerGrouping.ts",
    "src/components/session/sessionViewerAskUser.ts",
    "src/components/session/sessionViewerScroll.ts",
  ],
  "src/components/ArchivedSessionsPanel.tsx": ["src/components/session/ArchivedSessionsPanel.tsx"],
  "src/components/ExternalSessionList.tsx": ["src/components/session/ExternalSessionList.tsx"],
  "src/components/AssociationView.tsx": ["src/components/session/AssociationView.tsx"],
  "src/components/TaskBoardView.tsx": ["src/components/task/TaskBoardView.tsx"],
  "src/components/TaskDetailPanel.tsx": ["src/components/task/TaskDetailPanel.tsx"],
  "src/components/TaskCardRows.tsx": ["src/components/task/TaskCardRows.tsx"],
  // main 新增后按领域归入 common/（TaskCardText 已被 main 当死代码删除）
  "src/components/FloatingScroll.tsx": ["src/components/common/FloatingScroll.tsx"],
  "src/components/TaskTemplateDialog.tsx": ["src/components/task/TaskTemplateDialog.tsx"],
  "src/components/StageEditor.tsx": ["src/components/task/StageEditor.tsx"],
  "src/components/StageOptionsBar.tsx": ["src/components/task/StageOptionsBar.tsx"],
  "src/components/ScheduledAgentTaskDialog.tsx": [
    "src/components/task/ScheduledAgentTaskDialog.tsx",
    "src/components/task/ScheduledCronEditor.tsx",
  ],
  "src/components/WorktreeBranchSelector.tsx": ["src/components/task/WorktreeBranchSelector.tsx"],
  "src/components/FileTree.tsx": [
    "src/components/file/FileTree.tsx",
    "src/components/file/FileTreeRefreshButton.tsx",
    "src/components/file/useAgentConfigFlowTransitions.ts",
    "src/components/file/fileTreeTypes.ts",
    "src/components/file/icons.tsx",
    "src/components/file/AgentConfigPopover.tsx",
    "src/app/usePwaInstall.ts",
    "src/components/file/fileTreeEntries.tsx",
    "src/components/file/useFileTreeEffects.ts",
  ],
  "src/components/FileViewer.tsx": ["src/components/file/FileViewer.tsx"],
  "src/components/FileEditor.tsx": ["src/components/file/FileEditor.tsx"],
  "src/components/FileEditor.css": ["src/components/file/FileEditor.css"],
  "src/components/FileOperations.tsx": ["src/components/file/FileOperations.tsx"],
  "src/components/BinaryViewer.tsx": ["src/components/file/BinaryViewer.tsx"],
  "src/components/ImageViewer.tsx": ["src/components/file/ImageViewer.tsx"],
  "src/components/DocumentViewer.tsx": ["src/components/file/DocumentViewer.tsx"],
  "src/components/CodeViewer.tsx": ["src/components/file/CodeViewer.tsx"],
  "src/components/MarkdownViewer.tsx": ["src/components/file/MarkdownViewer.tsx"],
  "src/components/DiagramPreview.tsx": ["src/components/file/DiagramPreview.tsx"],
  "src/components/DiagramPreview.css": ["src/components/file/DiagramPreview.css"],
  "src/components/DefaultListView.tsx": [
    "src/components/file/DefaultListView.tsx",
    "src/components/file/Breadcrumbs.tsx",
    "src/components/file/listViewIcons.tsx",
  ],
  "src/components/ProjectAddPopover.tsx": ["src/components/file/ProjectAddPopover.tsx"],
  "src/components/CompactUploadProgress.tsx": ["src/components/file/CompactUploadProgress.tsx"],
  "src/components/SymlinkBadge.tsx": ["src/components/file/SymlinkBadge.tsx"],
  "src/components/GitStatusPanel.tsx": ["src/components/git/GitStatusPanel.tsx"],
  "src/components/GitDiffViewer.tsx": ["src/components/git/GitDiffViewer.tsx"],
  "src/components/GitHistoryPanel.tsx": ["src/components/git/GitHistoryPanel.tsx"],
  "src/components/RootGitContentView.tsx": ["src/components/root/RootGitContentView.tsx"],
  "src/components/RootWorktreeContentView.tsx": ["src/components/root/RootWorktreeContentView.tsx"],
  "src/components/RootRelatedContentView.tsx": ["src/components/root/RootRelatedContentView.tsx"],
  "src/components/AgentSelector.tsx": [
    "src/components/agent/AgentSelector.tsx",
    "src/components/agent/AgentSelectorErrorBanner.tsx",
    "src/components/agent/AgentSelectorMenuHeader.tsx",
    "src/components/agent/AgentSelectorParts.tsx",
  ],
  "src/components/AgentIcon.tsx": ["src/components/agent/AgentIcon.tsx"],
  "src/components/AgentMenuList.tsx": ["src/components/agent/AgentMenuList.tsx"],
  "src/components/AgentMemoryIndicator.tsx": ["src/components/agent/AgentMemoryIndicator.tsx"],
  "src/components/CodexRateLimitIndicator.tsx": ["src/components/agent/CodexRateLimitIndicator.tsx"],
  "src/components/ProviderModelSelect.tsx": ["src/components/agent/ProviderModelSelect.tsx"],
  "src/components/ModeSelector.tsx": ["src/components/agent/ModeSelector.tsx"],
  "src/components/ModeIcon.tsx": ["src/components/agent/ModeIcon.tsx"],
  "src/components/Login.tsx": ["src/components/account/Login.tsx"],
  "src/components/AuthGate.tsx": ["src/components/account/AuthGate.tsx"],
  "src/components/AccountPanel.tsx": ["src/components/account/AccountPanel.tsx"],
  "src/components/NodeManagerPanel.tsx": ["src/components/account/NodeManagerPanel.tsx"],
  "src/components/NodeSwitcher.tsx": ["src/components/account/NodeSwitcher.tsx"],
  "src/components/NodeBadgeHeader.tsx": ["src/components/account/NodeBadgeHeader.tsx"],
  "src/components/PanelShell.tsx": ["src/components/shell/PanelShell.tsx"],
  "src/components/BottomSheet.tsx": ["src/components/shell/BottomSheet.tsx"],
  "src/components/Toast.tsx": ["src/components/shell/Toast.tsx"],
  "src/components/DialogHost.tsx": ["src/components/shell/DialogHost.tsx"],
  "src/components/MainViewSwitcher.tsx": ["src/components/shell/MainViewSwitcher.tsx"],
  "src/components/RightSidebar.tsx": ["src/components/shell/RightSidebar.tsx"],
  "src/components/ErrorBoundary.tsx": ["src/components/shell/ErrorBoundary.tsx"],
  "src/components/OnboardingTour.tsx": ["src/components/shell/OnboardingTour.tsx"],
  "src/components/Select.tsx": ["src/components/common/Select.tsx"],
  "src/components/InlineTokenText.tsx": ["src/components/common/InlineTokenText.tsx"],
  "src/components/NoWorktreeIcon.tsx": ["src/components/common/NoWorktreeIcon.tsx"],
  "src/components/ExitIcon.tsx": ["src/components/common/ExitIcon.tsx"],
  "src/components/PromptEditor.tsx": ["src/components/editor/PromptEditor.tsx"],
  "src/components/ActionBar.tsx": ["src/components/action/ActionBar.tsx"],
  "src/components/diagramZoom.ts": ["src/shared/diagramZoom.ts"],
  "src/components/gitDiffModel.ts": ["src/shared/gitDiffModel.ts"],
  "src/components/markdownOutline.ts": ["src/shared/markdownOutline.ts"],
  "src/components/modelFiltering.ts": ["src/shared/modelFiltering.ts"],
  "src/components/rootBadgeStyle.ts": ["src/shared/rootBadgeStyle.ts"],
  "src/components/sessionOverlay.ts": ["src/shared/sessionOverlay.ts"],
  "src/services/api.ts": ["src/services/net/api.ts"],
  "src/services/base.ts": ["src/services/net/base.ts"],
  "src/services/connection.ts": ["src/services/net/connection.ts"],
  "src/services/controlPlane.ts": ["src/services/net/controlPlane.ts"],
  "src/services/nodeBase.ts": ["src/services/net/nodeBase.ts"],
  "src/services/nodeRegistry.ts": ["src/services/net/nodeRegistry.ts"],
  "src/services/prefix.ts": ["src/services/net/prefix.ts"],
  "src/services/rootNode.ts": ["src/services/net/rootNode.ts"],
  "src/services/e2ee.ts": ["src/services/net/e2ee.ts"],
  "src/services/authGate.ts": ["src/services/net/authGate.ts"],
  "src/services/error.ts": ["src/services/net/error.ts"],
  "src/services/bootstrap.ts": ["src/services/net/bootstrap.ts"],
  "src/services/replyPoller.ts": ["src/services/net/replyPoller.ts"],
  "src/services/nativeBridge.ts": ["src/services/platform/nativeBridge.ts"],
  "src/services/runtime.ts": ["src/services/platform/runtime.ts"],
  "src/services/pwaInstall.ts": ["src/services/platform/pwaInstall.ts"],
  "src/services/staleAssetRecovery.ts": ["src/services/platform/staleAssetRecovery.ts"],
  "src/services/reloadObserver.ts": ["src/services/platform/reloadObserver.ts"],
  "src/services/download.ts": ["src/services/platform/download.ts"],
  "src/services/clipboard.ts": ["src/services/platform/clipboard.ts"],
  "src/services/platformNavigation.ts": ["src/services/platform/platformNavigation.ts"],
  "src/services/nativeCacheControl.ts": ["src/services/platform/nativeCacheControl.ts"],
  "src/services/launcherNodeSync.ts": ["src/services/platform/launcherNodeSync.ts"],
  "src/services/preferences.ts": ["src/services/prefs/preferences.ts"],
  "src/services/appearance.ts": ["src/services/prefs/appearance.ts"],
  "src/services/fontSize.ts": ["src/services/prefs/fontSize.ts"],
  "src/services/sendShortcut.ts": ["src/services/prefs/sendShortcut.ts"],
  "src/services/onboarding.ts": ["src/services/prefs/onboarding.ts"],
  "src/services/directorySort.ts": ["src/services/prefs/directorySort.ts"],
  "src/services/storage.ts": ["src/services/prefs/storage.ts"],
  "src/services/context.ts": ["src/services/prefs/context.ts"],
  "src/services/scope.ts": ["src/shared/scope.ts"],
};

/** 逻辑路径对应的物理文件列表；未登记则返回原路径本身。 */
export function moduleFiles(logical) {
  return MODULES[logical] ?? [logical];
}

/** 按逻辑路径读取源码文本；拆分过的模块返回其全部物理文件的拼接。 */
export function readSource(logical) {
  return moduleFiles(logical)
    .map((file) => originalReadFileSync(path.join(WEB_ROOT, file), "utf8"))
    .join("\n");
}

/**
 * 把测试里的绝对路径 / file URL 归一成相对 web/ 的逻辑路径（POSIX 分隔符）。
 * 不是 web/ 下的文件（或参数是 fd 等非路径）返回 null。
 */
export function toLogical(target) {
  try {
    let abs;
    if (target instanceof URL) {
      abs = fileURLToPath(target);
    } else if (typeof target === "string") {
      abs = path.resolve(target);
    } else {
      return null;
    }
    const rel = path.relative(WEB_ROOT, abs);
    if (!rel || rel.startsWith("..") || path.isAbsolute(rel)) {
      return null;
    }
    return rel.split(path.sep).join("/");
  } catch {
    return null;
  }
}

/**
 * 供 ts-module-hook 用：把「解析到逻辑路径、但物理文件已移动」的单文件模块
 * 重定向到新的物理路径（绝对、无扩展名）。多文件（被拆分的 barrel）不重定向 ——
 * 它们的入口 shim 仍在原路径，能被正常解析。
 */
const LOGICAL_EXTENSIONS = [".ts", ".tsx", "/index.ts", "/index.tsx", ".js", ".mjs", ".json"];

export function redirectSingleFileModule(absNoExt) {
  const rel = toLogical(absNoExt);
  if (!rel) return null;
  for (const ext of LOGICAL_EXTENSIONS) {
    const files = MODULES[rel + ext];
    if (files && files.length === 1) {
      return path.join(WEB_ROOT, files[0].replace(/\.[^.]+$/, ""));
    }
  }
  return null;
}
