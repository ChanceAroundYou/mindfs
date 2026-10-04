import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

// 看板/工作台任务卡上三个推进类按钮的**给法**（2026-09 用户定的）。
//
//   还有下一段没执行 → 不给「完成」（收了尾就看不到下一段，等于替用户提前结束）
//   没有下一段       → 不给「执行」（推进不动，点了什么都不发生）
//   收尾流程进行中   → 三个都不给（清场马上就要拆 worktree）
//
// 判据一律跑组件里**真实的**源码，不是抄一份：抄一份的话，组件改了测试照样绿，
// 那样钉住的就只是「我自己写的那段逻辑对」，不是「界面上真的这么给」。
// 这几个门补上之后第一件事就是变异测试 —— 删掉任何一个全套照样绿，说明压根没有
// 测试守着它们。这条注释就是提醒下一个动它的人再变异一次。

const root = path.resolve(import.meta.dirname, "..");
const card = fs.readFileSync(path.join(root, "src/components/TaskCardRows.tsx"), "utf8");
const appTask = fs.readFileSync(path.join(root, "src/app/appTask.ts"), "utf8");

/**
 * 把 appTask.ts 里某个纯函数**原样**提出来跑，而不是抄它的逻辑。
 *
 * 取的是函数体（丢掉签名上的 TS 类型标注 —— new Function 只吃 JS），
 * 体内用到的兄弟函数作为参数喂进去。
 */
function loadPureFn(name, deps = {}) {
  const start = appTask.indexOf(`export function ${name}(`);
  if (start < 0) throw new Error(`appTask must export ${name}`);
  const open = appTask.indexOf("{", appTask.indexOf(")", start));
  const end = appTask.indexOf("\n}", open);
  if (!(open > start && end > open)) throw new Error(`${name} has no body to lift`);
  // 签名带 TS 类型标注（stage: StageTemplate | undefined): boolean），new Function 只吃 JS。
  // 切在第一个顶层 `{` 之前拿到整个签名，把标注连同返回值类型一起去掉。
  const openParen = appTask.indexOf("(", start);
  const params = appTask
    .slice(openParen + 1, open)
    .split(",")
    .map((part) => part.split(":")[0].trim())
    .join(", ");
  // deps 既要当 new Function 的形参名，又要真的传进去 —— 少了后者就是
  // 「isFinishStage is not a function」这种在函数体里才炸的错。
  const built = new Function(...Object.keys(deps), `return (${params}) => {${appTask.slice(open + 1, end)}\n};`);
  return built(...Object.values(deps));
}

const isFinishStageActive = loadPureFn("isFinishStageActive", { isFinishStage: loadPureFn("isFinishStage") });
const hasLaterStage = loadPureFn("hasLaterStage");
const isTerminalKanbanTask = loadPureFn("isTerminalKanbanTask");
// 卡片侧的推进门控（2026-10-03 补：showAdvance 以前完全不看当前段能不能走，
// 与详情面板给出相反答案，点下去是死按钮）。纯函数，直接从 appTask 取。
const canAdvanceCard = loadPureFn("canAdvanceCard");

// 卡片上那几行判据同样按原文跑一遍（用一个壳把前置变量喂进去）。
// 切片止于 statusText：往后就开始渲染了，new Function 吃不下 JSX。
const gateSrc = card
  .slice(
    card.indexOf("const terminal = isTerminalKanbanTask(task);"),
    card.indexOf("const statusText = taskStatusLabel"),
    card.indexOf("const canFinishWorktree ="),
  )
  // 这段里只有一处类型标注，去掉它 new Function 才吃得下。
  .replace(": WorktreeTagState", "");
assert.ok(gateSrc.length > 0, "the card's button gates must be reachable");
// canFinishWorktree 那几行在 statusText 之后，单独接上去（中间隔着标题文案那些
// 纯计算，跟按钮无关）。worktreeTagState 带了类型标注，去掉。
const finishGateSrc = card
  .slice(card.indexOf("const worktreeEnabled = task.create_worktree === true;"), card.indexOf("const numberLabel = task.task_number"))
  .replace(": WorktreeTagState", "");
assert.match(finishGateSrc, /const canFinishWorktree =/, "the finish gate must be inside the lifted slice");

// gateSrc 里用到 t() 取标题文案、worktreeTagState 取徽标档位，都不是按钮本身，
// 但同在一段里，一起喂进去。
const cardGates = new Function(
  "t",
  "isTerminalKanbanTask",
  "isFinishStageActive",
  "hasLaterStage",
  "canAdvanceCard",
  `return ((task) => {
${gateSrc}
${finishGateSrc}
return { canComplete, showAdvance, canFinishWorktree, canPause, canResume };
});`,
);
const gatesWithDeps = cardGates((key) => key, isTerminalKanbanTask, isFinishStageActive, hasLaterStage, canAdvanceCard);

const gatesOf = (task) => gatesWithDeps(task);


const finishStage = { role: "agent", kind: "worktree_finish" };
const worktreeTask = (extra = {}) => ({
  create_worktree: true,
  worktree_path: "/x/.worktree/t1",
  status: "waiting_user",
  current_stage_index: 0,
  stages: [{ role: "agent" }, { role: "agent" }],
  current_stage_status: "success",
  ...extra,
});

test("hasLaterStage reads the pointer against the stage list", () => {
  assert.equal(hasLaterStage({ current_stage_index: 0, stages: [{ role: "user" }, { role: "agent" }] }), true);
  assert.equal(hasLaterStage({ current_stage_index: 1, stages: [{ role: "user" }, { role: "agent" }] }), false);
  // 单段任务：指针 0 落在最后一段上，没有「下一段」。
  assert.equal(hasLaterStage({ current_stage_index: 0, stages: [{ role: "user" }] }), false);
  // 没有段（空任务）时不能算成「还有下一段」—— 那会让完成按钮凭空消失。
  assert.equal(hasLaterStage({ current_stage_index: 0, stages: [] }), false);
});

test("a task with stages left to run does not get 完成", () => {
  const gates = gatesOf(worktreeTask());
  assert.equal(gates.canComplete, false, "there is a next stage: completing now would hide it");
  assert.equal(gates.showAdvance, true, "and the button that does work should be there instead");
});

test("a task on its last stage does not get 执行", () => {
  const gates = gatesOf(worktreeTask({ current_stage_index: 1 }));
  assert.equal(gates.showAdvance, false, "there is nothing to advance into");
  assert.equal(gates.canComplete, true, "完成 is the only action left, so it must be there");
});

test("a running finish stage takes all three away", () => {
  // 指针后面**还得有段**，否则 showAdvance 本来就是 false（no more stages），
  // 测不出 finishActive 那一条守卫 —— 变异测试确认过：那时候去掉守卫照样绿。
  const gates = gatesOf(
    worktreeTask({ current_stage_index: 1, stages: [{ role: "agent" }, finishStage, { role: "agent" }] }),
  );
  assert.equal(gates.canComplete, false);
  assert.equal(gates.showAdvance, false, "advancing a stage during a teardown is work against a directory about to vanish");
  assert.equal(gates.canFinishWorktree, false, "re-running a finish would stack a second finish stage");
});

// 终态任务：完成/推进两个键照旧不给，但**收尾要给**（2026-10-04 用户要求）。
// 「完成」不给是对的 —— 收了尾就看不到后续；「执行」不给也是对的 —— 没有段可推进。
// 收尾反过来：任务跑完了 worktree 还留着没收，那正是它唯一有意义的时刻，
// 服务端 reviveTerminalTask 会把状态拉回 waiting_user 再让收尾段跑起来。
// 原断言是三条全不给，与新需求直接矛盾，所以钉的东西换了，不是删了换绿灯。
test("a terminal task gets 完成/执行 withheld but keeps the finish key", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    const gates = gatesOf(worktreeTask({ status, current_stage_index: 1 }));
    assert.equal(gates.canComplete, false, `${status}: completing would hide later stages`);
    assert.equal(gates.showAdvance, false, `${status}: there is nothing to advance into`);
    assert.equal(gates.canFinishWorktree, true, `${status}: the worktree is still holding unmerged work`);
  }
  // 反过来把 terminal 这条守卫真的去掉，上面那些门就塌了 —— 钉住「不给」的那些门
  // 在终态下依然生效，收尾键的豁免不是把三个键一起放开。
  const missingWorktree = gatesOf(worktreeTask({ status: "success", worktree_missing: true }));
  assert.equal(missingWorktree.canFinishWorktree, false, "a terminal task with a dead worktree still must not offer the finish key");
  const noAgentStage = gatesOf(worktreeTask({ status: "success", stages: [{ role: "user" }] }));
  assert.equal(noAgentStage.canFinishWorktree, false, "there is no agent stage to inherit agent/model from — the server would 409");
});

// 跑完之后指针还停在收尾段上，但清场已经把活干完了 —— 不该再显示「收尾中」。
test("a finished finish stage no longer counts as active", () => {
  assert.equal(
    isFinishStageActive(
      worktreeTask({ current_stage_index: 1, stages: [{ role: "agent" }, finishStage, { role: "agent" }] }),
    ),
    true,
  );
  assert.equal(
    isFinishStageActive(
      worktreeTask({
        status: "success",
        current_stage_index: 1,
        stages: [{ role: "agent" }, finishStage, { role: "agent" }],
      }),
    ),
    false,
  );
  // 指针不在收尾段上（普通 agent 段收工）也不是收尾中。
  assert.equal(isFinishStageActive(worktreeTask({ current_stage_index: 1 })), false);
  // 指针越界同理，否则一个坏 index 会让所有按钮凭空消失。
  assert.equal(isFinishStageActive(worktreeTask({ current_stage_index: 9, stages: [finishStage] })), false);
  // 指针落在**别的**段上（普通 agent 段收工）：那不是收尾中。收尾段被追加在
  // 尾巴上，所以正常流程里指针 0 只可能是首段 —— 这里显式摆一个反例，
  // 免得日后谁把判据写成「任务里有收尾段就算收尾中」。
  assert.equal(
    isFinishStageActive(worktreeTask({ current_stage_index: 0, stages: [{ role: "agent" }, finishStage] })),
    false,
  );
});

// 暂停/恢复跟这三档无关，但它们就在同一段判据里，一起钉住免得顺手改坏。
test("pause and resume still key on the stage actually running", () => {
  const running = gatesOf(worktreeTask({ status: "running", current_stage_status: "running" }));
  assert.equal(running.canPause, true);
  assert.equal(running.canResume, false);
  // waiting_user：那一段早跑完了，没什么可暂停的。
  assert.equal(gatesOf(worktreeTask()).canPause, false);
  const paused = gatesOf(worktreeTask({ status: "paused" }));
  assert.equal(paused.canPause, false);
  assert.equal(paused.canResume, true);
});

// 详情面板有 stage_runs，判得更准：那一段跑过就该按「最后一段」处理，
// 而不是「指针后面还有段」。两处口径不同是有意的（面板看得见执行记录）。
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");
assert.match(
  panel,
  /const canRunStage = index === runnableStageIndex[\s\S]{0,300}?!finishActive/,
  "the panel's run button must also be withheld while a finish stage is active",
);
assert.match(
  panel,
  /!isTerminalKanbanTask\(task\)\s*&&\s*!finishActive/,
  "the panel's finish button must be withheld for terminal tasks and a running finish alike",
);

console.log("task-button-gates.test.mjs: OK");
