import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";

// 跨项目工作台的契约（docs/workspace-design.md）。
// 项目内看板的契约在 task-board-view.test.mjs；扇出在 workspace-node-fanout.test.mjs；
// 数据新鲜度在 workspace-realtime-freshness.test.mjs。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/TaskBoardView.tsx", import.meta.url), "utf8");
const board = readFileSync(new URL("../src/components/workspace/WorkspaceBoard.tsx", import.meta.url), "utf8");
const attention = readFileSync(new URL("../src/components/workspace/WorkspaceAttentionBar.tsx", import.meta.url), "utf8");
const projectRow = readFileSync(new URL("../src/components/workspace/WorkspaceProjectRow.tsx", import.meta.url), "utf8");
const taskRow = readFileSync(new URL("../src/components/workspace/WorkspaceTaskRow.tsx", import.meta.url), "utf8");
const quick = readFileSync(new URL("../src/components/workspace/WorkspaceQuickLaunch.tsx", import.meta.url), "utf8");
const styles = readFileSync(new URL("../src/components/workspace/workspaceStyles.ts", import.meta.url), "utf8");
const storage = readFileSync(new URL("../src/app/appStorage.ts", import.meta.url), "utf8");
const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

// 1) 挂载优先级不变：工作台是 mainView 的 workspace 档位，有项目时项目看板胜出
assert.match(
  app,
  /const workspaceOpen = mainView === "workspace";/,
  "workspace is a mode of the single main-view state",
);
assert.match(
  view,
  /if \(workspaceOpen\) return workspacePanel;\s*\n\s*if \(!currentRootId\) return null;/,
  "the project board still wins when a project is open",
);

// 2) 新面板已经接管：旧组件与它的 import 都不该存在
assert.doesNotMatch(
  view,
  /WorkspaceKanban/,
  "TaskBoardView must render WorkspaceBoard, not the deleted WorkspaceKanban",
);
assert.ok(
  !existsSync(new URL("../src/components/WorkspaceKanban.tsx", import.meta.url)),
  "WorkspaceKanban.tsx must be deleted",
);
assert.match(view, /from "\.\/workspace\/WorkspaceBoard"/, "the new board must be the one mounted");

// 3) 节点色纪律（commit 14df0d7）：共享样式里不得出现任何字面 hex。
//    强调色只能来自节点色，选中底色只能是中性灰 —— 这两条是那次重构的核心。
assert.doesNotMatch(
  styles,
  /#[0-9a-fA-F]{3,8}\b/,
  "workspaceStyles.ts must stay token-only: no literal hex, accent comes from node color at runtime",
);
assert.match(
  styles,
  /background: active \? "var\(--node-row-selected-bg\)" : "transparent"/,
  "the active filter uses the neutral selected-row background, never a color",
);
assert.match(
  styles,
  /export const workspaceProjectHeaderHoverStyle[\s\S]*?background: "var\(--node-row-selected-bg\)"/,
  "hover/selected surfaces must use the neutral selected-row background",
);
assert.match(
  styles,
  /export const workspaceNodeDotStyle = \(color: string \| null\)[\s\S]*?background: color \|\| "var\(--text-secondary\)"/,
  "the node dot takes the node color and falls back to a neutral token when unknown",
);
// 节点色带透明度由 hexToRgbaApp 从运行时色值算出，不在样式文件里写死
assert.match(styles, /import \{ hexToRgbaApp \} from "\.\.\/\.\.\/app\/taskIcons"/, "node tint must reuse the shared rgba helper");

// 4) 信息架构：顶部「需要你」条带 + 按项目分组 + 底部快速发起。
//    唯一按状态分区的地方是顶部条带，它回答的是与项目无关的「现在该我做什么」。
assert.match(board, /<WorkspaceAttentionBar items=\{board\.blockedAll\}/, "the attention bar is fed the cross-project blocked list");
assert.match(board, /board\.projects\.map\(\(group\) => \(\s*<WorkspaceProjectRow/, "projects render as groups, not as a flat task list");
assert.match(board, /<WorkspaceQuickLaunch projects=\{board\.projects\}/, "quick launch stays pinned at the bottom");
assert.doesNotMatch(
  board,
  /const waiting = useMemo|const running = useMemo|const archive = useMemo/,
  "the old status partition (waiting/running/archive) must be gone",
);
// 条带没人等时整条收起，不占空间
assert.match(attention, /if \(items\.length === 0\) return null;/, "the attention bar collapses when nothing needs you");

// 5) 空项目必须出现：后端只返回有任务的项目，所以组以 managedRootIds 为基准建
assert.match(projectRow, /shown\.length === 0 \?/i, "an empty project still renders its own row");
assert.match(projectRow, /task\.workspaceEmptyProject/, "the empty project says so instead of vanishing");

// 6) 状态分区口径必须与 isTerminalKanbanTask 一致，终态任务不能被静默丢掉
assert.match(taskRow, /isTerminalKanbanTask\(task\)/, "task rows must use the shared terminal-status predicate");
assert.match(projectRow, /\[\.\.\.group\.active, \.\.\.group\.ended\]/, "a project shows active tasks before ended ones");

// 7) 就地操作仍在：完成/立刻跑/暂停/继续，都走 App 传来的统一回调
assert.match(taskRow, /onComplete\(item\)/, "complete stays inline");
assert.match(taskRow, /onRunNow\(item\)/, "run-now stays inline");
assert.match(taskRow, /onTogglePause\(item\)/, "pause/resume stays inline");

// 8) 快速发起必须用 PromptEditor（与任务侧其它输入统一），提交后手动清空
assert.match(quick, /import \{ PromptEditor \} from "\.\.\/PromptEditor"/, "the quick-launch field must reuse PromptEditor");
assert.match(quick, /<PromptEditor[\s\S]*?value=\{input\}[\s\S]*?onSend=\{submit\}[\s\S]*?sendDisabled=\{!input\.trim\(\) \|\| !effectiveKey\}/, "quick launch binds the editor value, Enter-to-send, and the empty guard");
// 编辑器常驻挂载（resetKey 不变不会重灌），提交后必须手动清一次
assert.match(quick, /editorRef\.current\?\.clear\(\);/, "quick launch must clear the editor after submitting");
// 项目增删后旧选中会悬空，回落到第一个
assert.match(quick, /options\.some\(\(option\) => option\.value === rootKey\) \? rootKey : options\[0\]\?\.value \|\| ""/, "a dangling project selection must fall back to the first project");

// 9) 窄屏：旧面板零响应式处理（没有任何 isMobile 分支），这里钉住三处该变的地方
assert.match(board, /const \{ isMobile \} = useResponsive\(\);/, "the board must read the viewport, not assume desktop");
assert.match(
  board,
  /<WorkspaceAttentionBar items=\{board\.blockedAll\} onOpenTask=\{onOpenTask\} isMobile=\{isMobile\} \/>/,
  "the attention bar must be told about the viewport",
);
assert.match(
  attention,
  /<div style=\{workspaceAttentionBarStyle\(isMobile\)\}>/,
  "the attention bar must switch layout on narrow screens",
);
assert.match(
  styles,
  /export const workspaceAttentionBarStyle = \(isMobile = false\)[\s\S]*?isMobile\s*\n\s*\? \{ display: "flex", flexDirection: "column"/,
  "a 220px card does not fit a 375px pane: the bar stacks instead of scrolling sideways",
);
assert.match(
  styles,
  /width: isMobile \? "100%" : "220px"/,
  "attention cards go full width on narrow screens",
);
assert.match(
  quick,
  /style=\{isMobile \? \{ \.\.\.workspaceQuickLaunchStyle, \.\.\.workspaceQuickLaunchMobileStyle \} : workspaceQuickLaunchStyle\}/,
  "quick launch stacks its project picker and input on narrow screens",
);
assert.match(
  quick,
  /isMobile \? \{ width: "100%" \} : \{ minWidth: "120px", maxWidth: "180px" \}/,
  "the project picker takes the full row on narrow screens",
);

// 10) 筛选与折叠状态跨会话记住（读时校验、非法值回退默认）
assert.match(storage, /const WORKSPACE_FILTER_STORAGE_KEY = "mindfs-workspace-filter";/, "the filter needs its own storage key");
assert.match(storage, /const WORKSPACE_COLLAPSED_STORAGE_KEY = "mindfs-workspace-collapsed";/, "collapsed groups need their own storage key");
assert.match(
  storage,
  /WORKSPACE_FILTERS\.includes\(saved as WorkspaceBoardFilter\) \? \(saved as WorkspaceBoardFilter\) : "all"/,
  "an illegal persisted filter must fall back to 「全部」",
);
assert.match(
  storage,
  /return new Set\(Array\.isArray\(parsed\) \? parsed\.filter\(\(v\): v is string => typeof v === "string"\) : \[\]\);/,
  "collapsed keys must tolerate corrupt storage",
);

// 11) 面板文案两端都要有，缺 key 会渲染成空白
for (const key of [
  "task.workspaceAttention",
  "task.workspaceFilterAll",
  "task.workspaceFilterActive",
  "task.workspaceFilterBlocked",
  "task.workspaceEmptyProject",
  "task.workspaceNoBlocked",
  "task.workspaceNoProjects",
  "task.workspaceSessions",
  "task.workspaceEnded",
  "task.workspaceExpand",
  "task.workspaceCollapse",
  "task.workspaceQuickLaunch",
  "task.quickLaunchPlaceholder",
  "task.quickLaunchSend",
]) {
  assert.ok(zh.includes(`"${key}"`), `${key} missing in zh-CN`);
  assert.ok(en.includes(`"${key}"`), `${key} missing in en-US`);
}

// 12) 随旧面板一起废弃的 key 必须两端都删干净（留着会让人以为还有那个分区）
for (const key of [
  "task.workspaceNothingWaiting",
  "task.workspaceNothingRunning",
  "task.workspaceAll",
  "task.workspaceRecent",
  "task.workspaceNoArchive",
]) {
  assert.ok(!zh.includes(`"${key}"`), `${key} should be gone from zh-CN`);
  assert.ok(!en.includes(`"${key}"`), `${key} should be gone from en-US`);
}

console.log("workspace-board.test.mjs: OK");
