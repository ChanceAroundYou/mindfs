import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 项目内四块看板的契约。跨项目工作台的契约已拆到 workspace-board.test.mjs。
// 2026-09 App.tsx 拆分：项目看板搬到 components/TaskBoardView.tsx，契约随文件走。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const bar = readFileSync(new URL("../src/components/ActionBar.tsx", import.meta.url), "utf8");
const board = readFileSync(new URL("../src/components/TaskBoardView.tsx", import.meta.url), "utf8");
const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

// 有项目时项目看板胜出，工作台只是「无项目」时的兜底。
assert.match(
  board,
  /if \(workspaceOpen\) return workspacePanel;\s*\n\s*if \(!currentRootId\) return null;/,
  "project board should win when a project is open; workspace is the no-project fallback",
);

// 工作台快速发起不再自己 createTask —— 它挑完项目 + 模板就打开看板那套新建任务面板，
// 模板/worktree/agent/附件只有一份实现。存活的唯一「无模板建任务」入口因此消失，
// 后端 stages[0].role === "user" 的要求改由 openTaskCreateDialog 选的模板保证。
assert.match(
  app,
  /const handleWorkspaceCreateTask = useCallback\(\(rootId: string, _nodeId: string, template: TaskTemplate\) => \{\s*\n\s*openTaskCreateDialog\(template, rootId\);/,
  "workspace quick launch must hand off to the shared create-task dialog, not build a second one",
);
// 面板得知道自己往哪个项目建：targetRootId 缺省才是当前项目（看板入口走这条）
assert.match(
  app,
  /const openTaskCreateDialog = useCallback\(\(template: TaskTemplate \| null, targetRootId\?: string\)/,
  "openTaskCreateDialog must accept the target project",
);
assert.match(
  app,
  /const rootId = edit\?\.targetRootId \|\| currentRootIdRef\.current;/,
  "saving must target the dialog's project, not whatever project happens to be selected",
);
assert.doesNotMatch(
  app,
  /createTask\(rootId, "", text, false, "new", "", getNodeIdForRoot\(rootId\)/,
  "the template-less createTask path must be gone",
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

console.log("task-board-view.test.mjs: OK");
