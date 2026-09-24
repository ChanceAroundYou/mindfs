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
  /return MAIN_VIEW_MODES\.includes\(saved as MainViewMode\) \? \(saved as MainViewMode\) : "board";/,
  "first run should fall back to the board (same as before the refactor)",
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
