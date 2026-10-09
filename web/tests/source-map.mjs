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
  // P6 拆分：FileTree.tsx → + icons.tsx（图标子组件）
  "src/components/file/FileTree.tsx": [
    "src/components/file/FileTree.tsx",
    "src/components/file/icons.tsx",
  ],

  // P2 拆分：file.ts → services/file/（入口 shim 仍在原路径）
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
  "src/components/SessionList.tsx": ["src/components/session/SessionList.tsx"],
  "src/components/SessionViewer.tsx": ["src/components/session/SessionViewer.tsx"],
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
  "src/components/ScheduledAgentTaskDialog.tsx": ["src/components/task/ScheduledAgentTaskDialog.tsx"],
  "src/components/WorktreeBranchSelector.tsx": ["src/components/task/WorktreeBranchSelector.tsx"],
  "src/components/FileTree.tsx": ["src/components/file/FileTree.tsx"],
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
  "src/components/DefaultListView.tsx": ["src/components/file/DefaultListView.tsx"],
  "src/components/ProjectAddPopover.tsx": ["src/components/file/ProjectAddPopover.tsx"],
  "src/components/CompactUploadProgress.tsx": ["src/components/file/CompactUploadProgress.tsx"],
  "src/components/SymlinkBadge.tsx": ["src/components/file/SymlinkBadge.tsx"],
  "src/components/GitStatusPanel.tsx": ["src/components/git/GitStatusPanel.tsx"],
  "src/components/GitDiffViewer.tsx": ["src/components/git/GitDiffViewer.tsx"],
  "src/components/GitHistoryPanel.tsx": ["src/components/git/GitHistoryPanel.tsx"],
  "src/components/RootGitContentView.tsx": ["src/components/root/RootGitContentView.tsx"],
  "src/components/RootWorktreeContentView.tsx": ["src/components/root/RootWorktreeContentView.tsx"],
  "src/components/RootRelatedContentView.tsx": ["src/components/root/RootRelatedContentView.tsx"],
  "src/components/AgentSelector.tsx": ["src/components/agent/AgentSelector.tsx"],
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
