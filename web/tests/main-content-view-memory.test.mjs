import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 2026-09 App.tsx 拆分：顶层存储键/常量移到 app/appSupport.tsx，契约随文件走。
// 2026-09 二次拆分：appSupport.tsx 拆成 appStorage / appPath / appSession / appTask / appMisc，
// 各断言改为读符号真正所在的那个模块，契约随文件走。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const appStorage = readFileSync(new URL("../src/app/appStorage.ts", import.meta.url), "utf8");
const appPath = readFileSync(new URL("../src/app/appPath.ts", import.meta.url), "utf8");
const panel = readFileSync(new URL("../src/components/DefaultListView.tsx", import.meta.url), "utf8");
const switcher = readFileSync(new URL("../src/components/MainViewSwitcher.tsx", import.meta.url), "utf8");

// 设计契约见 docs/main-view-switching-design.md：主区内容由**唯一一个**全局状态决定。
assert.match(
  appStorage,
  /const MAIN_VIEW_STORAGE_KEY = "mindfs-main-view";/,
  "main view should persist under its own key",
);
assert.match(
  app,
  /const \[mainView, setMainView\] = useState<MainViewMode>\(/,
  "main view should be a single app-level state",
);
assert.match(
  appPath,
  /export type MainViewMode = "workspace" \| "board" \| "files" \| "chat";/,
  "the four modes are workspace / board / files / chat",
);
assert.match(
  appStorage,
  /return MAIN_VIEW_MODES\.includes\(saved as MainViewMode\) \? \(saved as MainViewMode\) : "workspace";/,
  "first run should land on the workspace (工作台), not the board",
);
// 旧键迁移同理：只有 file-browser 有意义，其余落到工作台。
// 存量用户存的 mindfs-main-view 仍然照读 —— 记住用户自己的选择。
assert.match(
  appStorage,
  /const migrated: MainViewMode = legacy === "file-browser" \? "files" : "workspace";/,
  "legacy migration should fall back to the workspace too",
);

// 旧的「按项目记忆 + 全局兜底」必须彻底消失：那正是「点目录突然跳到看板」的根因
assert.doesNotMatch(
  app,
  /mainContentViewByRoot|defaultMainContentView|setMainContentViewForRoot/,
  "per-project view memory and the task-kanban fallback must be gone",
);
assert.doesNotMatch(
  app,
  /mainViewPreferenceByRootRef|setMainViewPreferenceForRoot/,
  "the write-only view preference ref must be gone",
);

// 主区容器模式由 mainView 派生
assert.match(
  app,
  /const currentMainContentView: MainContentViewMode =[\s\S]*?mainView === "files"\s*\?\s*"file-browser"\s*:\s*"task-kanban"/,
  "board/workspace feed the kanban container, files feeds the file list",
);
assert.match(
  app,
  /const workspaceOpen = mainView === "workspace";/,
  "the workspace is a mode, not a side effect of having no project open",
);

// 显式切换入口
assert.match(
  app,
  /const switchMainView = useCallback\(\(mode: MainViewMode\) => \{[\s\S]*?lastNonChatViewRef\.current = mode;[\s\S]*?setMainView\(mode\);/,
  "switching should remember the last non-chat mode and set the single state",
);
assert.match(
  app,
  /<MainViewSwitcher[\s\S]*?value=\{mainView\}[\s\S]*?onChange=\{handleMainViewSwitcherChange\}/,
  "the sidebar switcher should be wired to the single state",
);
assert.match(
  switcher,
  /\["workspace", "board", "files", "chat"\]/,
  "the switcher exposes exactly the four modes",
);

// 第二真相源必须消失：主区菜单里不再有视图切换
assert.doesNotMatch(
  panel,
  /onViewModeChange|isViewMenuOpen/,
  "the main-view menu must not offer a second way to switch views",
);

// 自动恢复会话只在 chat 模式下发生（否则就是「随手跳到会话」）
assert.match(
  app,
  /if \(mainViewRef\.current !== "chat"\) \{\s*return false;\s*\}/,
  "bound-session auto restore should be gated on the chat mode",
);

// 只有左树里点「名字」会主动切到 files；程序化打开目录保持当前模式
assert.match(
  app,
  /if \(params\.switchToFiles === true\) switchMainView\("files"\);/,
  "only explicit tree clicks switch to the files view",
);
assert.match(
  app,
  /onSelectDir=\{\(e, r\) =>[\s\S]*?switchToFiles: true,[\s\S]*?\n            \}/,
  "folder-name clicks (not project-name clicks) declare switchToFiles",
);
assert.doesNotMatch(
  app,
  /onSelectRoot=\{[^}]*switchToFiles/,
  "project-name clicks must not force the files view",
);

// 会话面板只在对话态显示：判据是 mainView，不是 selectedSession
// （否则看板/工作台背后还挂着会话面板；而「切面板就清会话」是治标且丢选中）
assert.match(
  app,
  /const showSessionPane = mainView === "chat" && !!selectedSession;/,
  "the session pane must be gated on the main view, not on merely having a session",
);
assert.match(
  app,
  /display: showSessionPane \? "flex" : "none",[\s\S]*?\{sessionView\}[\s\S]*?display: showSessionPane \? "none" : "flex",[\s\S]*?\{workspaceView\}/,
  "both main-area panes must share one predicate so exactly one is visible",
);
assert.doesNotMatch(
  app,
  /handleMainViewTabChange/,
  "the session-clearing tab handler must be gone",
);
assert.match(
  app,
  /const handleMainViewSwitcherChange = useCallback\(\(mode: MainViewMode\) => \{[\s\S]*?setDrawerOpenForRoot\(rootID, false\);[\s\S]*?switchMainView\(mode\);/,
  "the switcher closes the drawer and switches the pane without touching the selection",
);

// 悬浮框只属于文件态
assert.match(
  app,
  /isOpen=\{isDrawerOpen && mainView === "files"\}/,
  "the floating session drawer may only exist in the files view",
);

// 主面板模式进 URL：按钮高亮与面板显示必须同源恢复
assert.match(
  appPath,
  /view\?: MainViewMode;/,
  "URL state should carry the main view",
);
assert.match(
  appPath,
  /if \(next\.view\) params\.set\("view", next\.view\);/,
  "the view must be serialized into the URL",
);

// 主区渲染优先级链必须让 mainView 生效优先于残留的 file/gitDiff state。
// 少了这两道门控，「开着文件点看板/工作台」就是按钮亮了、画面不动
// （R1 被绕过：状态机切了，渲染链在 else if (file) 就 return 了）。
assert.match(
  app,
  /\} else if \(file && currentMainContentView === "file-browser"\) \{/,
  "the file branch must yield to the main view, or an open file blocks the board",
);
assert.match(
  app,
  /\} else if \(gitDiff && currentMainContentView === "file-browser"\) \{/,
  "the git-diff branch must yield to the main view too",
);
assert.doesNotMatch(
  app,
  /\} else if \((file|gitDiff)\) \{/,
  "the ungated file/gitDiff branches must not come back",
);
// 门控必须用派生值而不是裸 mainView：onboarding 覆盖时 mainView 可能仍是 files，
// 但 currentMainContentView 已经是 task-kanban —— 换成 mainView 会让引导里的看板
// 被一个打开着的文件顶掉。
assert.doesNotMatch(
  app,
  /\} else if \((file|gitDiff)\) && mainView === "files"\) \{/,
  "gate on currentMainContentView, not on the raw mainView (onboarding override)",
);

// 无论当前选中什么（四态按钮都常亮可点），切换器都没有基于路径的禁用逻辑
assert.doesNotMatch(
  switcher,
  /disabled/,
  "the switcher must never disable a mode based on the current path",
);
assert.match(
  switcher,
  /onClick=\{\(\) => onChange\(mode\)\}/,
  "each switcher button forwards its mode straight through",
);

// 「从没点过目录就补上项目根」的兜底不能吃掉正打开着的文件/diff：
// open_dir 会 setFile(null)，补目录等于把用户刚看的文件抹掉。
// 少了这两个前置条件，「切走再切回文件」就会退回目录列表而不是原文件。
assert.match(
  app,
  /if \(mode === "files" && rootID && !file && !gitDiff && !mainEntriesRef\.current\.length/,
  "the root-dir top-up must stand down while a file or diff is open",
);

// 深链恢复：URL 里的 view 是主面板模式的唯一真相源。
// 恢复文件时不能被 open 的无条件 switchMainView("files") 盖回去 ——
// 与会话恢复的 preserveMainView 是同一约定（App.tsx 两处都传同一个判据）。
assert.match(
  app,
  /if \(!params\?\.preserveMainView\) \{\s*switchMainView\("files"\);/,
  "open must honour preserveMainView instead of always forcing the files view",
);
assert.match(
  app,
  /preservePluginQuery: true,\s*\n\s*preserveMainView: !!urlState\.view && urlState\.view !== "files",/,
  "cold-start deep-link restore must let the URL view win over the restored file",
);
assert.match(
  app,
  /preservePluginQuery: true,\s*\n\s*preserveMainView: !!state\.view && state\.view !== "files",/,
  "popstate deep-link restore must let the URL view win too",
);
