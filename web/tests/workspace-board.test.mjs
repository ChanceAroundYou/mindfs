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
assert.match(board, /<WorkspaceQuickLaunch projects=\{board\.projects\} templates=\{templates\} onPick=\{onCreateTask\}/, "quick launch is a trigger that hands off to the shared dialog");
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
assert.match(taskRow, /isTerminalKanbanTask\(task\)/, "task rows still need the terminal predicate for their own buttons");

// 7) 就地操作仍在：完成/立刻跑/暂停/继续，都走 App 传来的统一回调
assert.match(taskRow, /onComplete\(item\)/, "complete stays inline");
assert.match(taskRow, /onRunNow\(item\)/, "run-now stays inline");
assert.match(taskRow, /onTogglePause\(item\)/, "pause/resume stays inline");

// 8) 快速发起必须是「一个按钮 + 点开的面板」，不能再常驻挂编辑器 ——
//    常驻的编辑器会自己抢焦点，工作台一进来焦点就被它吃掉。
assert.doesNotMatch(quick, /PromptEditor/, "quick launch must not mount an editor at all — that is the focus steal");
assert.match(quick, /const \[open, setOpen\] = useState\(false\);/, "the panel state must start closed");
assert.match(quick, /aria-expanded=\{open\}/, "the trigger must expose its expanded state");
assert.match(quick, /\{open \? \([\s\S]*?\) : null\}/, "the panel must not exist in the tree until the button is pressed");
// 面板基于看板的新建任务面板，只多一个「选项目」
assert.match(quick, /onPick\(target\.rootId, target\.nodeId, template\)/, "picking must hand the chosen project + template to the shared dialog");
assert.match(quick, /onPick: \(rootId: string, nodeId: string, template: TaskTemplate\) => void;/, "and that callback must carry a real template");
// 面板自己有两个下拉：项目 + 模板
assert.equal(quick.match(/<Select/g).length, 2, "the panel needs exactly two pickers: project and template");
// 项目增删后旧选中会悬空，回落到第一个
assert.match(quick, /options\.some\(\(option\) => option\.value === rootKey\) \? rootKey : options\[0\]\?\.value \|\| ""/, "a dangling project selection must fall back to the first project");
// 点外面 / Esc 都要能直接丢掉这个临时选择
assert.match(quick, /document\.addEventListener\("mousedown", onPointerDown\)/, "click-away must close the panel");
assert.match(quick, /if \(event\.key === "Escape"\) setOpen\(false\);/, "Escape must close the panel");
// 会话数不再显示：用户明确说「不需要显示有多少会话」
assert.doesNotMatch(projectRow, /workspaceSessions/, "the session-count badge must be gone");
assert.doesNotMatch(hook, /sessionCount|sessionCounts/, "and the data layer must not plumb it either");

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
assert.match(
  quick,
  /\.\.\.\(isMobile \? \{ \.\.\.workspaceQuickLaunchStyle, \.\.\.workspaceQuickLaunchMobileStyle \} : \{\}\)/,
  "the quick-launch panel stacks on narrow screens",
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
]) {
  assert.ok(zh.includes(`"${key}"`), `${key} missing in zh-CN`);
  assert.ok(en.includes(`"${key}"`), `${key} missing in en-US`);
}

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
