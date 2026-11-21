import assert from "node:assert/strict";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { taskCardActions } from "../src/components/task/taskCardActions.ts";

// 看板/工作台任务卡上操作键的**给法**（2026-11 会话模型）。
//
//   正在跑       → 只给「暂停」，不给「完成」（活还在动）
//   暂停         → 给「继续」
//   等待你/未开始 → 给「完成」
//   终态          → 完成/暂停/继续都不给；worktree 还在就只给「收尾」
//
// 判据一律跑组件里**真实的**源码，不是抄一份：抄一份的话，组件改了测试照样绿，
// 那样钉住的就只是「我自己写的那段逻辑对」，不是「界面上真的这么给」。
// 改这几个门时先做变异测试 —— 删掉任何一个全套照样绿，说明压根没有测试守着它们。

const root = path.resolve(import.meta.dirname, "..");
const card = fs.readFileSync(path.join(root, "src/components/TaskCardRows.tsx"), "utf8");
const appTask = fs.readFileSync(path.join(root, "src/app/appTask.ts"), "utf8");

/** 把 appTask.ts 里某个纯函数**原样**提出来跑，而不是抄它的逻辑。 */
function loadPureFn(name, deps = {}) {
  const start = appTask.indexOf(`export function ${name}(`);
  if (start < 0) throw new Error(`appTask must export ${name}`);
  const open = appTask.indexOf("{", appTask.indexOf(")", start));
  const end = appTask.indexOf("\n}", open);
  if (!(open > start && end > open)) throw new Error(`${name} has no body to lift`);
  const openParen = appTask.indexOf("(", start);
  const params = appTask
    .slice(openParen + 1, open)
    .split(",")
    .map((part) => part.split(":")[0].trim())
    .join(", ");
  const built = new Function(...Object.keys(deps), `return (${params}) => {${appTask.slice(open + 1, end)}\n};`);
  return built(...Object.values(deps));
}

const isTerminalKanbanTask = loadPureFn("isTerminalKanbanTask");

// 卡片上那几行判据同样按原文跑一遍（用一个壳把前置变量喂进去）。
// 切片从 status 派生开始，到 numberLabel 之前：中间正好是 terminal / running /
// pause / resume / worktree 三档 / canFinishWorktree / canComplete，全是按钮判据。
const gateSrc = card
  .slice(card.indexOf("const terminal = isTerminalKanbanTask(task);"), card.indexOf("const numberLabel = task.task_number"))
  // 这一段里只有一处类型标注，去掉它 new Function 才吃得下。
  .replace(": WorktreeTagState", "");
assert.ok(gateSrc.length > 0, "the card's button gates must be reachable");
assert.match(gateSrc, /const actions = taskCardActions\(task\);/, "the card gates must come from the shared action list");
assert.match(gateSrc, /const canComplete = actions\.includes\("complete"\)/, "the complete gate must be inside the lifted slice");
assert.match(gateSrc, /const canFinishWorktree = actions\.includes\("finish-worktree"\)/, "the finish gate must be inside the lifted slice");

// gateSrc 里用到 t() 取标题文案，不是按钮本身，但同在一段里，一起喂进去。
const cardGates = new Function(
  "t",
  "isTerminalKanbanTask",
  "taskStatusLabel",
  "taskCardActions",
  `return ((task) => {
${gateSrc}
return { canComplete, canPause, canResume, canReopen, canFinishWorktree, worktreeTagState };
});`,
);
const gatesWithDeps = cardGates((key) => key, isTerminalKanbanTask, (status) => status, taskCardActions);

const gatesOf = (task) => gatesWithDeps(task);

const worktreeTask = (extra = {}) => ({
  create_worktree: true,
  worktree_path: "/x/.worktree/t1",
  status: "waiting_user",
  ...extra,
});

// 完成只要求「不在跑」：活还在动时先暂停；终态没有后续可完成。
test("完成 is offered exactly when the task is neither running nor terminal", () => {
  for (const status of ["pending", "waiting_user", "paused"]) {
    assert.equal(gatesOf(worktreeTask({ status })).canComplete, true, `${status}: 完成 must be offered`);
  }
  for (const status of ["running", "success", "fail", "cancelled"]) {
    assert.equal(gatesOf(worktreeTask({ status })).canComplete, false, `${status}: 完成 must be withheld`);
  }
});

test("暂停 only exists while a turn is running, 继续 only while paused", () => {
  const running = gatesOf(worktreeTask({ status: "running" }));
  assert.equal(running.canPause, true);
  assert.equal(running.canResume, false);
  const paused = gatesOf(worktreeTask({ status: "paused" }));
  assert.equal(paused.canPause, false);
  assert.equal(paused.canResume, true);
  for (const status of ["pending", "waiting_user"]) {
    const gates = gatesOf(worktreeTask({ status }));
    assert.equal(gates.canPause, false, `${status}: nothing is running to pause`);
    assert.equal(gates.canResume, false, `${status}: not paused`);
  }
});

// 收尾要有可拆的 worktree：目录不在了给的是「重建」，没有 worktree 可拆。
// **终态也给**：任务跑完但 worktree 还留着没收，那正是收尾唯一有意义的场景；
// 服务端会把它复活成 running 再起收尾回合。
test("a terminal task with a live worktree keeps the finish key", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    assert.equal(gatesOf(worktreeTask({ status })).canFinishWorktree, true, `${status}: worktree still holds unmerged work`);
  }
});

test("the finish key needs a directory that still exists", () => {
  assert.equal(gatesOf(worktreeTask({ worktree_missing: true })).canFinishWorktree, false, "nothing left to tear down");
  assert.equal(gatesOf(worktreeTask({ worktree_path: "" })).canFinishWorktree, false, "no worktree path");
  assert.equal(gatesOf(worktreeTask({ create_worktree: false })).canFinishWorktree, false, "the task never asked for a worktree");
});

// 收尾键与徽标同一口径：目录消失是 gold standard，读「已收尾」而不是红色警报。
test("the badge state follows the directory, not the status", () => {
  assert.equal(gatesOf(worktreeTask()).worktreeTagState, "enabled");
  assert.equal(gatesOf(worktreeTask({ worktree_path: "" })).worktreeTagState, "none");
  assert.equal(gatesOf(worktreeTask({ worktree_path: "", worktree_built: true })).worktreeTagState, "finished");
  for (const status of ["success", "fail", "cancelled", "running", "waiting_user"]) {
    assert.equal(gatesOf(worktreeTask({ worktree_missing: true, status })).worktreeTagState, "finished", `${status} + 目录不在 = 已收尾`);
  }
});

// 详情面板不能再自己抄一份 gate（抄一份就会和卡片给出相反答案，2026-10-03 实测过）。
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");
assert.doesNotMatch(panel, /const canRunStage = /, "the detail panel must not own a stage run gate");
assert.doesNotMatch(panel, /finishActive/, "the detail panel must not duplicate a finish-active gate");

// 回到待审核（2026-11-19 用户：完成/取消的任务要能继续对话）：卡片与详情面板
// 都只在终态给这个键，一处接线断了用户就只能删卡重建。
test("回到待审核 is offered exactly for terminal tasks, on card and panel", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    assert.equal(gatesOf(worktreeTask({ status })).canReopen, true, `${status}: reopen must be offered`);
  }
  for (const status of ["pending", "waiting_user", "paused", "running"]) {
    assert.equal(gatesOf(worktreeTask({ status })).canReopen, false, `${status}: nothing closed to reopen`);
  }

  // 详情面板不再自己抄条件：菜单项直接来自 taskCardActions(task)，与卡片同源。
  const panel = fs.readFileSync(path.join(root, "src/components/task/TaskDetailPanel.tsx"), "utf8");
  assert.match(panel, /taskCardActions\(task\)\.map/, "the detail menu must read the shared action list");
  const hooks = fs.readFileSync(path.join(root, "src/components/task/useTaskDetailPanel.ts"), "utf8");
  assert.match(hooks, /moveTask\(task\.root_id, task\.id, "reopen"/, "the panel must call the reopen route");
  assert.match(hooks, /moveTask\(task\.root_id, task\.id, "cancel"/, "the panel must call the cancel route");
  assert.match(hooks, /archiveTask\(task\.root_id, task\.id, archived, nodeId\)/, "the panel must call the archive route");

  const crud = fs.readFileSync(path.join(root, "src/services/task/crud.ts"), "utf8");
  assert.match(crud, /"complete" \| "cancel" \| "fail" \| "reopen"/, "moveTask must expose the reopen action");

  const board = fs.readFileSync(path.join(root, "src/app/useTaskBoard.ts"), "utf8");
  assert.match(
    board,
    /action === "cancel" \|\| action === "reopen" \|\| action === "rebuild-worktree"/,
    "reopen moves a card between columns, so the board must refetch",
  );
});

console.log("task-button-gates.test.mjs: OK");
