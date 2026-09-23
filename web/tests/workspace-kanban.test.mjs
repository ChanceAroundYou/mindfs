import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const panel = readFileSync(new URL("../src/components/WorkspaceKanban.tsx", import.meta.url), "utf8");
const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

// 总面板只在「无项目 + 任务视图」时接管，有项目时仍走项目内看板
assert.match(
  app,
  /const workspaceOpen = mainView === "workspace";/,
  "workspace should only own the task view when no project is open",
);
assert.match(
  app,
  /const kanbanTaskPanel = workspaceOpen \? \(\n    <WorkspaceKanban[\s\S]*?\n  \) : currentRootId \? \(/,
  "project board should win when a project is open; workspace is the no-project fallback",
);

// 汇总数据来自 /api/tasks/overview，且任务详情变化后自动重拉（否则操作后卡片状态会滞留）
assert.match(
  app,
  /if \(!workspaceOpen\) return;[\s\S]*?fetchTasksOverview\(currentRootNodeIdRef\.current \|\| undefined\)[\s\S]*?if \(!cancelled\) setWorkspaceOverview/,
  "workspace should fetch the overview with a cancel guard",
);
assert.match(
  app,
  /\}, \[workspaceOpen, taskDetailsById\]\);/,
  "overview should refresh when task details change (WS broadcast or local action)",
);

// 快速发起必须自带 user 首段 + 任务名：后端在无模板时要求 stages[0].role === "user"
assert.match(
  app,
  /createTask\(rootId, "", text, false, "new", "", getNodeIdForRoot\(rootId\), \{[\s\S]*?stages: \[\{ name: "", role: "user" \} as StageTemplate\]/,
  "quick launch should create a task with an explicit user stage and no template",
);

// 跨项目操作必须按卡片自带的 root_id 派发，不能落到当前项目
assert.match(
  app,
  /onComplete=\{\(item\) => \{ void handleMoveKanbanTask\(item\.task, "complete"\); \}\}/,
  "complete should act on the card's own root",
);
assert.match(
  app,
  /onOpenSession=\{\(item, sessionKey\) => \{ handleTaskSessionDrawerOpen\(sessionKey, item\.root_id, item\.task\.id\); \}\}/,
  "opening a session should use the card's own root",
);
assert.match(
  app,
  /onOpenDetail=\{\(item\) => \{ void openWorkspaceProject\(item\.root_id\)\.then\(\(\) => setSelectedKanbanTaskId\(item\.task\.id\)\); \}\}/,
  "opening a detail should switch project first, then select the task",
);

// 工作台入口收敛到左栏底部的四态切换器（见 docs/main-view-switching-design.md）
assert.match(
  app,
  /<MainViewSwitcher[\s\S]*?onChange=\{handleMainViewSwitcherChange\}/,
  "the workspace should be reachable from the sidebar switcher",
);

// 分区：等待你 / 运行中 / 归档，归档默认只显示最近几条，可切全部
assert.match(
  panel,
  /const waiting = useMemo\(\s*\(\) => items\.filter\(\(item\) => item\.task\.status === "waiting_user" \|\| item\.task\.status === "pending"\)/,
  "waiting section should cover waiting_user and pending",
);
assert.match(
  panel,
  /const shown = showAll \? archive : archive\.slice\(0, 5\);/,
  "archive should default to the most recent entries until switched to all",
);

// 面板文案两端都要有，缺 key 会渲染成空白
for (const key of [
  "task.workspaceQuickLaunch",
  "task.quickLaunchPlaceholder",
  "task.quickLaunchSend",
  "task.workspaceAll",
  "task.workspaceRecent",
  "task.workspaceNothingWaiting",
  "task.workspaceNothingRunning",
  "task.workspaceNoArchive",
]) {
  assert.ok(zh.includes(`"${key}"`), `${key} missing in zh-CN`);
  assert.ok(en.includes(`"${key}"`), `${key} missing in en-US`);
}
