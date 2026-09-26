import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";

// 跨项目工作台的契约（docs/workspace-design.md）。
// 项目内看板的契约在 task-board-view.test.mjs；扇出在 workspace-node-fanout.test.mjs；
// 数据新鲜度在 workspace-realtime-freshness.test.mjs。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const hook = readFileSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/TaskBoardView.tsx", import.meta.url), "utf8");
const board = readFileSync(new URL("../src/components/workspace/WorkspaceBoard.tsx", import.meta.url), "utf8");
const attention = readFileSync(new URL("../src/components/workspace/WorkspaceAttentionBar.tsx", import.meta.url), "utf8");
const projectRow = readFileSync(new URL("../src/components/workspace/WorkspaceProjectRow.tsx", import.meta.url), "utf8");
const taskRow = readFileSync(new URL("../src/components/workspace/WorkspaceTaskRow.tsx", import.meta.url), "utf8");
// 卡片两行（标题行 + 操作行）抽成了看板和工作台共用的组件，契约跟着文件走。
const cardRows = readFileSync(new URL("../src/components/TaskCardRows.tsx", import.meta.url), "utf8");
const quick = readFileSync(new URL("../src/components/workspace/WorkspaceQuickLaunch.tsx", import.meta.url), "utf8");
const styles = readFileSync(new URL("../src/components/workspace/workspaceStyles.ts", import.meta.url), "utf8");
const services = readFileSync(new URL("../src/services/tasks.ts", import.meta.url), "utf8");
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
  /background: active \? "var\(--node-row-selected-bg\)" : "transparent"/,
  "hover/selected surfaces must use the neutral selected-row background",
);
// 项目名前没有色点：名字本身已经是节点色（workspaceProjectNameButtonStyle），
// 再配一个同色圆点等于把同一个信息说两遍，还占了名字的缩进。
assert.doesNotMatch(
  styles,
  /workspaceNodeDotStyle/,
  "the node dot is gone — the project name already carries the node color",
);
assert.doesNotMatch(
  projectRow,
  /workspaceNodeDotStyle/,
  "the project row must not render a color dot before the name",
);
// 节点色带透明度由 hexToRgbaApp 从运行时色值算出，不在样式文件里写死
assert.match(styles, /import \{[^}]*hexToRgbaApp[^}]*\} from "\.\.\/\.\.\/app\/taskIcons"/, "node tint must reuse the shared rgba helper");
// 条带是跨项目的，每张卡得按**自己**的任务找节点色，不能拿一个全局值
assert.match(
  board,
  /<WorkspaceAttentionBar[\s\S]*?getNodeColor=\{getNodeColor\}/,
  "the attention bar needs the node-color resolver to tint each card",
);
assert.match(
  attention,
  /style=\{workspaceAttentionCardStyle\(getNodeColor\(item\.root_id\), isMobile\)\}/,
  "an attention card must be tinted by the node of its own project, not a single global color",
);
assert.match(
  view,
  /getNodeColor=\{getDisplayNodeColor\}/,
  "the view must hand the board App's stable node-color resolver",
);
// 取不到节点色时回退中性 token，不猜一个颜色
assert.match(
  styles,
  /return \/\^#\[0-9a-fA-F\]\{3,8\}\$\/\.test\(hex\) \? hexToRgbaApp\(hex, alpha\) : `var\(--node-badge-bg\)`/,
  "an unknown node color must fall back to a neutral token",
);

// 4) 信息架构：顶部「需要你」条带 + 按项目分组 + 底部快速发起。
//    唯一按状态分区的地方是顶部条带，它回答的是与项目无关的「现在该我做什么」。
assert.match(board, /<WorkspaceAttentionBar items=\{board\.blockedAll\}/, "the attention bar is fed the cross-project blocked list");
assert.match(board, /board\.projects\.map\(\(group\) => \(\s*<WorkspaceProjectRow/, "projects render as groups, not as a flat task list");
assert.match(board, /<WorkspaceQuickLaunch projects=\{board\.projects\} templates=\{templates\} onPick=\{onCreateTask\} \/>/, "quick launch is a trigger that hands off to the shared dialog");
// 快速发起在工具栏里，且排在刷新键**之前**（紧贴其左边）
const quickAt = board.indexOf("<WorkspaceQuickLaunch");
const refreshAt = board.indexOf("title={t(\"common.refresh\")}");
assert.ok(quickAt > -1 && refreshAt > -1 && quickAt < refreshAt, "quick launch must sit in the toolbar, immediately left of the refresh button");
assert.doesNotMatch(
  board,
  /const waiting = useMemo|const running = useMemo|const archive = useMemo/,
  "the old status partition (waiting/running/archive) must be gone",
);
// 条带没人等时整条收起，不占空间
assert.match(attention, /if \(items\.length === 0\) return null;/, "the attention bar collapses when nothing needs you");

// 5) 没有任务的项目不出现 —— 筛选后是空壳的、连「全部」下都没有任务的，都不渲染。
//    组仍以 managedRootIds 建（后端只返回有任务的项目），只是建完按匹配到的任务收窄。
assert.match(hook, /\.filter\(\(group\) => group\.tasks\.length > 0\);/, "task-less projects must be dropped from every filter, including 「全部」");
assert.doesNotMatch(projectRow, /task\.workspaceEmptyProject/, "the empty-project placeholder is gone with the empty-project rows");
assert.doesNotMatch(board, /task\.workspaceEmptyProject/, "and the board must not render it either");

// 6) 「进行中」= 执行中 + 待审核，仅此两态。非终态还含 queued / paused（没在跑也没人等），
//    混进来之后筛选名不副实；而一旦选了「进行中」，行里就只能有这两类 —— 渲染层
//    不再补已完成的任务（那属于「全部」）。这正是「进行时里面全都是已完成」的病根。
assert.match(
  hook,
  /if \(filter === "active"\) return task\.status === "running" \|\| isBlockedTask\(task\);/,
  "the active filter must mean running + waiting-for-you, nothing else",
);
assert.match(hook, /if \(filter === "blocked"\) return isBlockedTask\(task\);/, "the blocked filter is waiting_user + pending");
assert.match(hook, /tasks: bucket\.filter\(\(item\) => matchesFilter\(item\.task, filter\)\)/, "the row list must be exactly what the filter matched");
assert.doesNotMatch(projectRow, /\[\.\.\.group\.active, \.\.\.group\.ended\]/, "ended tasks must not be appended under any filter");

// 7) 就地操作仍在：完成/立刻跑/暂停/继续，都走 App 传来的统一回调。
//    卡片两行已抽进共享的 TaskCardRows（看板同一份），所以这三件事由它保证，
//    工作台只负责把 onMove 透下去。
assert.match(cardRows, /onMove\(task, "complete"\)/, "complete stays inline");
assert.match(cardRows, /onMove\(task, "run-now"\)/, "run-now stays inline");
assert.match(cardRows, /onMove\(task, "pause"\)/, "pause stays inline");
assert.match(cardRows, /onMove\(task, "resume"\)/, "resume stays inline");
assert.match(taskRow, /onMove=\{\(_cardTask, action\) => onMove\(item, action\)\}/, "the move is reported against the workbench item, not the bare task");

// 8) 快速发起必须**一步到位**：按钮直接打开新建任务面板，不能再夹一层小弹窗。
//    两层弹窗意味着两次点击，而且第二层里真正能改的东西全被挡在后面。
//    面板侧的「项目」下拉由 allowProjectSwitch 控制，只在工作台入口出现。
assert.doesNotMatch(quick, /PromptEditor/, "quick launch must not mount an editor of its own");
assert.doesNotMatch(quick, /useState|useRef|setOpen|<Select|<div/, "the launch must be a plain trigger — no local panel state, no pickers, no second dialog");
assert.match(quick, /onClick=\{\(\) => onPick\(group\.rootId, group\.nodeId, template\)\}/, "one click hands the project + template straight to the shared create dialog");
assert.match(quick, /data-onboarding="task-create"/, "the onboarding anchor must survive the rewrite");
assert.match(app, /allowProjectSwitch: Boolean\(targetRootId\)/, "only the workspace entry gets the project dropdown; the board entry has none");
assert.match(app, /taskInlineEdit\.allowProjectSwitch && taskCreateProjectOptions\.length > 1/, "the dropdown only renders when there is a choice to make");
// 换项目时 worktree 三个开关按项目分别记：还原回那个项目那份，第一次去则取它的偏好
assert.match(app, /createWorktreePerRoot/, "worktree prefs must be stashed per project across switches");
// 分支列表 / 偏好存取都跟目标项目而不是 currentRootId，否则工作台发起时会串到当前项目
assert.match(app, /const rootId = edit\.targetRootId \|\| currentRootId \|\| "";[\s\S]*?loadTaskWorktreeBranches\(rootId\)/, "worktree branches must load for the panel's target project, not the current one");
assert.match(app, /const rootId = edit\.targetRootId \|\| currentRootIdRef\.current \|\| "";[\s\S]*?saveTaskCreateWorktreePreference\(rootId/, "worktree prefs must persist against the panel's target project");

// 8b) 选中态守卫不能在工作台上把刚打开的详情关掉：工作台选中的任务往往属于别的项目，
//     而 kanbanTasks 按 currentRootId 过滤，天然不含它们。
assert.match(
  app,
  /if \(workspaceOpenRef\.current && taskDetailsByIdRef\.current\[selectedKanbanTaskId\]\) return;/,
  "a cross-project selection made on the workbench must survive the stale-selection guard",
);

// 9) 窄屏：旧面板零响应式处理（没有任何 isMobile 分支），这里钉住三处该变的地方
assert.match(board, /const \{ isMobile \} = useResponsive\(\);/, "the board must read the viewport, not assume desktop");
assert.match(
  board,
  /<WorkspaceAttentionBar items=\{board\.blockedAll\} onOpenTask=\{onOpenTask\} isMobile=\{isMobile\} getNodeColor=\{getNodeColor\} \/>/,
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

// 9b) 层级：项目名用左侧项目列表那套徽章（中性灰底 + 主体色字），
//     任务是小卡片。两行不再共用同一个灰底 —— 那正是「看起来很丑」的病根。
assert.match(
  styles,
  /import \{ rootBadgeButtonStyle \} from "\.\.\/rootBadgeStyle"/,
  "the project name must reuse the sidebar's root-badge style, not invent its own chrome",
);
assert.match(
  styles,
  /export const workspaceProjectNameButtonStyle[\s\S]*?\.\.\.rootBadgeButtonStyle,[\s\S]*?color: String\(color \|\| ""\)\.trim\(\) \|\| "var\(--text-primary\)"[\s\S]*?cursor: "pointer"/,
  "project name = neutral badge background + the project's own node color, and it is the only jump target",
);
assert.match(
  taskRow,
  /style=\{\{ \.\.\.taskCardSurfaceStyle\(false\), padding: "8px" \}\}/,
  "the task card must reuse the kanban card surface, otherwise the two drift apart again",
);
// 卡片不含任务正文：工作台只扫读，正文去项目看板。
// 正文是 TaskCardRows 的 children，工作台不传 —— 所以这一层连正文相关的 prop 都没有。
assert.doesNotMatch(taskRow, /taskFirstInput|InlineTokenText|firstInput/, "the workbench card must not render task input text");
assert.doesNotMatch(cardRows, /taskFirstInput|InlineTokenText|expandedTaskInputIds/, "the shared card rows must not depend on the task body — the body arrives as children");
assert.match(
  taskRow,
  /<TaskCardRows[\s\S]*?\/>\s*<\/div>/,
  "the workbench card renders the shared rows with no children, so the body row is absent",
);
assert.match(
  taskRow,
  /showStatus\s*\n/,
  "the workbench has no columns, so it always shows the (colored) status text",
);
// 状态色只有一份定义，看板和工作台都从它拿
assert.match(
  cardRows,
  /color: taskStatusColor\(task\.status \|\| ""\)/,
  "the status text is tinted from the shared status-color helper",
);
assert.match(
  taskRow,
  /showStatus/,
  "the workbench card asks for the status text",
);

// 9c) 只有项目名能跳，整个头不能：以前整行都是热区，空白处/计数/色点都成了跳转陷阱。
const headerJsx = projectRow.slice(projectRow.indexOf("<div style={workspaceProjectHeaderStyle}>"), projectRow.indexOf("</section>"));
assert.doesNotMatch(headerJsx, /role="button"[\s\S]*?onClick=\{\(\) => onOpenProject/, "the whole project header must not be a click target");
assert.match(projectRow, /onClick=\{openProject\}/, "the project name button is what opens the project board");
assert.match(projectRow, /onClick=\{\(\) => onToggle\(group\.key\)\}/, "the chevron toggles collapse");
// 折叠键刻意无框无底：只看得见箭头，热区仍靠 40×24 撑（12px 图标点不准）
assert.match(
  styles,
  /export const workspaceProjectToggleStyle[\s\S]*?border: "none"[\s\S]*?background: "transparent"/,
  "the collapse toggle must have no border and no fill — only the arrow shows",
);
assert.match(
  styles,
  /export const workspaceProjectToggleStyle[\s\S]*?minWidth: "40px"[\s\S]*?height: "24px"/,
  "the collapse target must stay a comfortable 40x24, not a 12px icon",
);
// 任务排成卡片网格，不再是一条条平铺的行；列宽对齐看板单列的 220px
assert.match(styles, /export const workspaceTaskGridStyle[\s\S]*?gridTemplateColumns: "repeat\(auto-fill, minmax\(220px, 1fr\)\)"/, "tasks lay out as cards wide enough to show the name");

// 9d) 点任务卡留在工作台：不许再走 openWorkspaceProject（那会切项目 + 切看板）
assert.match(
  view,
  /onOpenTask=\{\(item\) => \{ void openWorkspaceTaskDetail\(item\); \}\}/,
  "clicking a workbench task must not navigate to the project board",
);
assert.doesNotMatch(
  view,
  /openWorkspaceProject\(item\.root_id\)\.then/,
  "the old jump-then-select path is gone",
);
assert.match(
  app,
  /fetchTaskDetails\(rootId, \{ taskNumber \}, item\.nodeId\)/,
  "the detail panel needs stage_runs/events, which overview does not return — fetch that one task by number",
);
assert.match(
  services,
  /if \(typeof filters\?\.taskNumber === "number" && filters\.taskNumber > 0\) params\.set\("task_number", String\(filters\.taskNumber\)\)/,
  "the single-task fetch must go through the existing task_number filter",
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
  "task.workspaceNoBlocked",
  "task.workspaceNoProjects",
  "task.workspaceEnded",
  "task.workspaceExpand",
  "task.workspaceCollapse",
  "task.workspaceQuickLaunch",
  "task.workspaceSelectProject",
  "task.quickLaunchPlaceholder",
  "task.quickLaunchSend",
  "task.workspaceProjectCount",
]) {
  assert.ok(zh.includes(`"${key}"`), `${key} missing in zh-CN`);
  assert.ok(en.includes(`"${key}"`), `${key} missing in en-US`);
}

// 11b) 文案本身：键名可以不变，值必须跟着变。
//      「快速发起」→「新建任务」（点开就是新建任务面板，名字得说清楚是哪件事）；
//      「待处理」→「待审核」—— task.status.waitingUser / task.column.waitingUser
//      早就是「待审核」，工作台这个筛选键是唯一的异类。
assert.match(zh, /"task\.workspaceQuickLaunch": "新建任务"/, "the launch button says 新建任务, not 快速发起");
assert.match(en, /"task\.workspaceQuickLaunch": "New task"/, "en-US launch label follows");
assert.match(zh, /"task\.workspaceFilterBlocked": "待审核"/, "the blocked filter must read 待审核, matching the status and column labels");
assert.match(en, /"task\.workspaceFilterBlocked": "Awaiting review"/, "en-US blocked filter matches the column label");
assert.doesNotMatch(zh, /"task\.workspaceFilterBlocked": "待处理"/, "待处理 was the odd one out");
assert.match(zh, /"task\.status\.waitingUser": "待审核"/, "status label stays 待审核");
assert.match(zh, /"task\.column\.waitingUser": "待审核"/, "column label stays 待审核");

// 11c) 面板标题：工作台不属于任何项目，顶部不能继续显示当前选中的项目名面包屑。
//       换成视图名 + 项目数，和 MainViewSwitcher 的「工作台/看板/文件/对话」对齐。
const listView = readFileSync(new URL("../src/components/DefaultListView.tsx", import.meta.url), "utf8");
assert.match(listView, /workspaceMode \? \(/, "the header must branch on workspace mode");
assert.match(listView, /\{workspaceMode \? \([\s\S]*?t\("view\.workspace"\)[\s\S]*?t\("task\.workspaceProjectCount", \{ count: workspaceProjectCount \}\)[\s\S]*?\) : \(\s*<Breadcrumbs/, "workspace mode shows the view name, everything else keeps the breadcrumb");
assert.match(
  app,
  /workspaceMode=\{workspaceOpen\}/,
  "only the workspace view opts out of the project-name breadcrumb",
);
assert.match(
  app,
  /workspaceProjectCount=\{workspaceBoard\.projects\.length\}/,
  "the title reports how many projects are on the board",
);
// 工作台态没有面包屑，就也不该有上传进度条（那是文件模式的东西）
assert.match(listView, /\{!workspaceMode && uploadProgress \? \(/, "the file-upload progress bar belongs to file mode, not the workbench");

// 12) 随旧面板一起废弃的 key 必须两端都删干净（留着会让人以为还有那个分区）
for (const key of [
  "task.workspaceEmptyProject",
  "task.workspaceSessions",
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
