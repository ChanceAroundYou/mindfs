import assert from "node:assert/strict";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { taskActionButtons, taskCardActions } from "../src/components/task/taskCardActions.ts";

// 看板/工作台任务卡上操作键的**给法**（2026-11 会话模型）。
//
//   正在跑       → 只给「终止」（真停会话与工作流，落回待处理）
//   暂停         → 给「继续」
//   未开始       → 给「开始」+「取消」（还没有产出可交付）
//   待处理        → 给「完成」+「取消」
//   终态          → 完成/暂停/继续都不给；worktree 还在就只给「收尾」
//
// 判据一律跑 `taskCardActions` 这个**真实**纯函数，不是抄一份：抄一份的话，
// 实现改了测试照样绿，那样钉住的就只是「我自己写的那段逻辑对」。
// 2026-11-22 起卡片与详情 header 读同一份列表，所以这份判据同时守着两处。
//
// 徽标那一段仍从 TaskCardRows 的源码里原样提出来跑（它是渲染期变量，不是纯函数）。
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

// worktree 徽标三档：从「建过没有 / 目录还在不在」那几行原样切出来跑。
// 切片从 worktreeEnabled 开始，到 worktreeTagTitle 之前 —— 正好是 enabled /
// finished / none 三档的判定。
const tagSrc = card
  .slice(card.indexOf("const worktreeEnabled = task.create_worktree === true;"), card.indexOf("const worktreeTagTitle ="))
  .replace(": WorktreeTagState", "");
assert.ok(tagSrc.length > 0, "the card's worktree badge states must be reachable");
const badgeOf = new Function("task", `return ((task) => {
${tagSrc}
return worktreeTagState;
})(task);`);

const gatesOf = (task) => {
  const actions = taskCardActions(task);
  return {
    canComplete: actions.includes("complete"),
    canStart: actions.includes("start"),
    canTerminate: actions.includes("terminate"),
    canPause: actions.includes("pause"),
    canResume: actions.includes("resume"),
    canReopen: actions.includes("reopen"),
    canFinishWorktree: actions.includes("finish-worktree"),
  };
};

const plainTask = (extra = {}) => ({ status: "waiting_user", create_worktree: false, ...extra });
const worktreeTask = (extra = {}) => plainTask({
  create_worktree: true,
  worktree_path: "/x/.worktree/t1",
  ...extra,
});

test("终态判定覆盖完成 / 失败 / 取消", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    assert.equal(isTerminalKanbanTask(plainTask({ status })), true, status);
  }
  for (const status of ["pending", "running", "paused", "waiting_user"]) {
    assert.equal(isTerminalKanbanTask(plainTask({ status })), false, status);
  }
});

test("正在跑只给终止，不给暂停 / 完成", () => {
  const gates = gatesOf(plainTask({ status: "running" }));
  assert.equal(gates.canTerminate, true);
  assert.equal(gates.canPause, false, "暂停不会停掉会话：界面说停了、活还在跑");
  assert.equal(gates.canComplete, false, "a running task first has to terminate");
  assert.equal(gates.canResume, false);
});

test("未开始给开始 + 取消，不给完成", () => {
  const gates = gatesOf(plainTask({ status: "pending" }));
  assert.equal(gates.canStart, true);
  assert.equal(gates.canComplete, false, "还没有任何产出可交付");
  assert.equal(gates.canTerminate, false, "没在跑，没有可终止的东西");
  const actions = taskCardActions(plainTask({ status: "pending" }));
  assert.deepEqual(actions, ["start", "cancel"]);
});

test("暂停给继续，恢复之后又回到完成", () => {
  const paused = gatesOf(plainTask({ status: "paused" }));
  assert.equal(paused.canResume, true);
  assert.equal(paused.canPause, false);
  const waiting = gatesOf(plainTask({ status: "waiting_user" }));
  assert.equal(waiting.canComplete, true);
  assert.equal(waiting.canResume, false, "nothing to resume once the task is back to waiting");
});

test("终态不再给完成 / 暂停 / 继续，改给返回与归档", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    const gates = gatesOf(plainTask({ status }));
    assert.equal(gates.canComplete, false, status);
    assert.equal(gates.canPause, false, status);
    assert.equal(gates.canResume, false, status);
    assert.equal(gates.canReopen, true, status);
    assert.ok(taskCardActions(plainTask({ status })).includes("archive-task"), status);
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
  assert.equal(badgeOf(worktreeTask()), "enabled");
  assert.equal(badgeOf(worktreeTask({ worktree_path: "" })), "none");
  assert.equal(badgeOf(worktreeTask({ worktree_path: "", worktree_built: true })), "finished");
  for (const status of ["success", "fail", "cancelled", "running", "waiting_user"]) {
    assert.equal(badgeOf(worktreeTask({ worktree_missing: true, status })), "finished", `${status} + 目录不在 = 已收尾`);
  }
  assert.equal(badgeOf(plainTask({ worktree_path: "/x" })), "none", "create_worktree 关着就不该因为 path 有值而显示有树");
});

test("卡片与详情都不自己抄条件：两处都读 taskActionButtons", () => {
  const panel = fs.readFileSync(path.join(root, "src/components/task/TaskDetailPanel.tsx"), "utf8");
  assert.match(card, /const buttons = taskActionButtons\(task\);/, "the card gates must come from the shared action list");
  assert.match(panel, /taskActionButtons\(task\)\.map/, "the flat detail header must read the same action list");
  // 卡片里不再有 terminal / canComplete / canReopen 这些手算门。
  for (const dead of ["const terminal =", "canComplete", "canReopen", "canPause", "canResume", "canFinishWorktree"]) {
    assert.ok(!card.includes(dead), `the card must not recompute ${dead}`);
    assert.ok(!panel.includes(dead), `the panel must not recompute ${dead}`);
  }
});

test("这四个动作都在面板里接到了真实处理函数", () => {
  const panel = fs.readFileSync(path.join(root, "src/components/task/TaskDetailPanel.tsx"), "utf8");
  const hook = fs.readFileSync(path.join(root, "src/components/task/useTaskDetailPanel.ts"), "utf8");
  for (const fn of ["reopenTask", "cancelTask", "archiveTaskCard", "unarchiveTaskCard", "finishWorktree", "pauseTask", "startTask", "terminateTask"]) {
    assert.ok(hook.includes(`${fn}`), `the panel hook must expose ${fn}`);
  }
  // 卡片与详情读同一份动作列表，所以每个键都得在 header 上接到真实处理函数。
  assert.match(panel, /start: \(\) => void panel\.startTask\(\)/, "the start key is wired to the hook");
  assert.match(panel, /terminate: \(\) => void panel\.terminateTask\(\)/, "the terminate key is wired to the hook");
});

test("看板把跨列动作当作重取信号", () => {
  const board = fs.readFileSync(path.join(root, "src/app/useTaskBoard.ts"), "utf8");
  for (const key of ["start", "terminate", "cancel", "reopen", "rebuild-worktree"]) {
    assert.match(
      board,
      new RegExp(`action === "${key}"`),
      `${key} moves a card between columns or changes worktree state, so the board must refetch`,
    );
  }
});

console.log("task-button-gates.test.mjs: OK");
