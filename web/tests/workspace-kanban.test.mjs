import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const bar = readFileSync(new URL("../src/components/ActionBar.tsx", import.meta.url), "utf8");
const panel = readFileSync(new URL("../src/components/WorkspaceKanban.tsx", import.meta.url), "utf8");
// 2026-09 App.tsx 拆分：项目看板搬到 components/TaskBoardView.tsx，契约随文件走。
const board = readFileSync(new URL("../src/components/TaskBoardView.tsx", import.meta.url), "utf8");
const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

// 总面板只在「无项目 + 任务视图」时接管，有项目时仍走项目内看板
assert.match(
  app,
  /const workspaceOpen = mainView === "workspace";/,
  "workspace should only own the task view when no project is open",
);
// 有项目时项目看板胜出，工作台只是「无项目」时的兜底。
// 2026-09 App.tsx 拆分：这条优先级判定搬到 TaskBoardView 内（workspaceOpen 早退 + currentRootId 守卫）。
assert.match(
  board,
  /if \(workspaceOpen\) return workspacePanel;\s*\n\s*if \(!currentRootId\) return null;/,
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
  board,
  /onComplete=\{\(item\) => \{ void handleMoveKanbanTask\(item\.task, "complete"\); \}\}/,
  "complete should act on the card's own root",
);
assert.match(
  board,
  /onOpenSession=\{\(item, sessionKey\) => \{ handleTaskSessionDrawerOpen\(sessionKey, item\.root_id, item\.task\.id\); \}\}/,
  "opening a session should use the card's own root",
);
assert.match(
  board,
  /onOpenDetail=\{\(item\) => \{ void openWorkspaceProject\(item\.root_id\)\.then\(\(\) => setSelectedKanbanTaskId\(item\.task\.id\)\); \}\}/,
  "opening a detail should switch project first, then select the task",
);

// 2026-09 App.tsx 拆分守卫：看板 JSX 必须留在 TaskBoardView，App 侧只转发。
// 一旦有人把那段 JSX 抄回 App，这里立刻失败。
assert.match(board, /data-onboarding="task-board"/, "看板容器应在视图组件里");
assert.match(board, /<WorkspaceKanban/, "视图组件应自己渲染跨项目工作台");
assert.doesNotMatch(
  app,
  /data-onboarding="task-board"/,
  "看板 JSX 不应再在 App 里内联实现（应转发给 TaskBoardView）",
);
assert.doesNotMatch(
  app,
  /const kanbanStageColumns:[\s\S]{0,4000}?data-onboarding="task-board"/,
  "App 只构造列数据，不渲染看板结构",
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

// 项目看板四块布局（未开始/执行中/待审核/已结束）—— 末列是「已结束」，失败并入取消。
// 这段曾被别的改动悄悄拆回 done+failed 两列，加断言钉住。
assert.match(
  app,
  /const kanbanStageColumns:[\s\S]*?t\("task\.column\.pending"\)[\s\S]*?t\("task\.column\.running"\)[\s\S]*?t\("task\.column\.waitingUser"\)[\s\S]*?t\("task\.column\.ended"\)/,
  "the board should have exactly four blocks, ending with 已结束",
);
assert.doesNotMatch(
  app,
  /name: t\("task\.column\.failed"\)/,
  "失败/取消 must be merged into the 已结束 block, not its own column",
);
assert.match(
  app,
  /t\("task\.column\.ended"\)[\s\S]*?key: "success"[\s\S]*?key: "cancelled"[\s\S]*?task\.status === "fail" \|\| task\.status === "cancelled"/,
  "已结束 must group 完成 and 取消, with 失败 folded into 取消",
);
// 每块固定高度 + 完成分组默认展开
assert.match(
  board,
  /gridAutoRows: isMobile \? "36dvh" : undefined,/,
  "mobile blocks should keep a fixed height",
);
assert.match(
  app,
  /const \[collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups\] = useState<Set<string>>\(\(\) => new Set\(\)\);/,
  "completion groups should start expanded",
);

// 会话界面只在对话态（主区）与文件态（悬浮框）出现；看板/工作台不得有输入区，
// 但移动端呼出左右侧栏的按钮必须留下（否则进了这两个界面就再也开不出侧栏）。
assert.match(
  app,
  /hideComposer=\{mainView === "board" \|\| mainView === "workspace"\}/,
  "the conversation composer must not render in the board or workspace views",
);
assert.match(
  bar,
  /if \(hideComposer\) \{\s*return isMobile \? \(/,
  "hiding the composer must still render the mobile sidebar toggles",
);
assert.match(
  bar,
  /\{sidebarsSwapped \? mobileSessionSidebarButton : mobileFileSidebarButton\}[\s\S]*?\{sidebarsSwapped \? mobileFileSidebarButton : mobileSessionSidebarButton\}/,
  "both sidebar toggles must survive the composer-less bar",
);
assert.match(
  app,
  /onSessionClick=\{\(\) => \{[\s\S]*?if \(!canOpenSessionDrawer\) return;/,
  "the drawer toggle must reuse canOpenSessionDrawer instead of recomputing a narrower predicate",
);
assert.match(
  app,
  /const isBoundSessionInMain =[\s\S]*?mainView === "chat";/,
  "「session is in the main pane」must require the chat mode, or the files-view drawer becomes unreachable",
);

// 四块的文案是定死的：未开始 / 执行中 / 待审核 / 已结束，已结束内分 完成 / 取消
for (const [key, label] of [
  ["task.column.pending", "未开始"],
  ["task.column.running", "执行中"],
  ["task.column.waitingUser", "待审核"],
  ["task.column.ended", "已结束"],
  ["task.group.completed", "完成"],
  ["task.group.cancelled", "取消"],
]) {
  assert.ok(
    zh.includes(`"${key}": "${label}"`),
    `${key} should read 「${label}」`,
  );
}

// 快速发起必须用 PromptEditor（与任务侧其它输入统一），不能再退回裸 <input>。
assert.match(
  panel,
  /import \{ PromptEditor \} from "\.\/PromptEditor"/,
  "the quick-launch field must reuse PromptEditor",
);
assert.match(
  panel,
  /<PromptEditor[\s\S]*?value=\{quickInput\}[\s\S]*?onSend=\{submitQuick\}[\s\S]*?sendDisabled=\{!quickInput\.trim\(\)\}/,
  "quick launch must bind the editor value, Enter-to-send, and the empty guard",
);
assert.doesNotMatch(
  panel,
  /placeholder=\{t\("task\.quickLaunchPlaceholder"\)\}[\s\S]{0,200}onKeyDown/,
  "quick launch must not keep the old raw <input> Enter handler",
);
// 编辑器常驻挂载（resetKey 不变不会重灌），提交后必须手动清一次，否则上次输入留在框里。
assert.match(
  panel,
  /quickEditorRef\.current\?\.clear\(\);/,
  "quick launch must clear the editor after submitting",
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
  "task.column.ended",
]) {
  assert.ok(zh.includes(`"${key}"`), `${key} missing in zh-CN`);
  assert.ok(en.includes(`"${key}"`), `${key} missing in en-US`);
}

// 模板子看板不得丢弃终态任务：#11(cancelled)/#13(success) 曾因此在「新功能」下整张消失，
// 连「已结束」列都进不去。「已结束」列唯一的任务来源就是 success/fail/cancelled，
// 筛选阶段再滤一道终态，列就必然是空的。
assert.match(
  app,
  /setKanbanTasks\(filtered\);/,
  "template filtering should narrow by template id only, not drop terminal tasks",
);
assert.doesNotMatch(
  app,
  /filtered\.filter\(isUnfinishedKanbanTask\)/,
  "no extra 'unfinished' filter — it silently empties the 已结束 column in sub-boards",
);
assert.doesNotMatch(
  app,
  /import[^;]*isUnfinishedKanbanTask[^;]*from "\.\/app\/appTask"/,
  "isUnfinishedKanbanTask should be gone once nothing calls it",
);
// —— 2026-09-24 两处回归守卫 ——

// 1) 「已完成」分组曾整组消失
//    App 侧构造已结束列的 groups 时带 .filter(g => g.tasks.length > 0)，空组被删；
//    而 TaskBoardView 用「groups 非空」决定走不走分组渲染 —— 于是「已完成」没任务、
//    「已取消」有任务时，前者连标题都不渲染，列头却仍按 tasks.length 显示两状态总和。
//    观感就是「列头有数字、里面是空的 / 找不到该组」。
assert.doesNotMatch(
  app,
  /\}\]\.filter\(\(group\) => group\.tasks\.length > 0\)/,
  "已结束列的 groups 不能滤掉空分组，否则「已完成」会整组消失",
);

// 2) 全部看板与子看板的卡片必须是同一套 DOM
//    以前用 isAllTaskTemplateFilter 把卡片劈成两套：全部看板有标题条，子看板没有，
//    #编号 与输入顶格同行、worktree badge 又在输入行右侧重复渲染一份。
//    已统一为「以全部看板为准」，卡片区不该再按筛选态分叉。
const cardStart = board.indexOf("<article");
const cardEnd = board.indexOf("</article>", cardStart);
const cardJsx = board.slice(cardStart, cardEnd);
assert.doesNotMatch(
  cardJsx,
  /isAllTaskTemplateFilter/,
  "看板卡片不应再按「全部/子」筛选态分叉渲染（已统一为全部看板样式）",
);
assert.doesNotMatch(
  cardJsx,
  /!isAllTaskTemplateFilter && taskNumberLabel/,
  "编号只由标题条渲染一次，不该在输入行里再来一份",
);

// 3) worktree badge 曾经渲染两份（标题条一份、输入行右侧一份），只留标题条那份
assert.equal(
  (cardJsx.match(/taskWorktreeTagStyle\(taskWorktreeEnabled\)/g) || []).length,
  1,
  "worktree badge 在卡片里只应渲染一次",
);
assert.match(cardJsx, /style=\{taskWorktreeTagStyle\(taskWorktreeEnabled\)\}/, "标题条应保留 worktree badge");

console.log("workspace-kanban.test.mjs: OK");
