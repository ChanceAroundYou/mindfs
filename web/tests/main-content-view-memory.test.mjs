import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const panel = readFileSync(new URL("../src/components/DefaultListView.tsx", import.meta.url), "utf8");
const switcher = readFileSync(new URL("../src/components/MainViewSwitcher.tsx", import.meta.url), "utf8");

// 设计契约见 docs/main-view-switching-design.md：主区内容由**唯一一个**全局状态决定。
assert.match(
  app,
  /const MAIN_VIEW_STORAGE_KEY = "mindfs-main-view";/,
  "main view should persist under its own key",
);
assert.match(
  app,
  /const \[mainView, setMainView\] = useState<MainViewMode>\(/,
  "main view should be a single app-level state",
);
assert.match(
  app,
  /export type MainViewMode = "workspace" \| "board" \| "files" \| "chat";/,
  "the four modes are workspace / board / files / chat",
);
assert.match(
  app,
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
  /<MainViewSwitcher[\s\S]*?value=\{mainView\}[\s\S]*?onChange=\{switchMainView\}/,
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

// 只有左树点击会主动切到 files；程序化打开目录保持当前模式
assert.match(
  app,
  /if \(params\.switchToFiles === true\) switchMainView\("files"\);/,
  "only explicit tree clicks switch to the files view",
);
assert.match(
  app,
  /switchToFiles: true,/,
  "tree root clicks should declare switchToFiles",
);
