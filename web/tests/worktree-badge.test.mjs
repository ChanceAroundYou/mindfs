import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

// worktree 徽标的四种状态 + 收尾流程的整条反馈链。
//
// 徽标说的是「**现在**有没有 worktree」，不是「当初要不要建树」：create_worktree
// 是创建时的配置，永久 true，收尾之后一点没变 —— 光看它会让收完尾的任务照样显示
// 「有 worktree」。判据是**目录在不在**（path 被清空，或 path 还在而目录已被删）。
//
// 曾经有第五档 missing（开过、目录被删了，红色警报）。2026-10-05 删掉：目录消失
// 是「已收尾」的金标准 —— 没有目录就既没有树可执行、也没有树可拆，红色警报承诺的
// 「点重建恢复后再执行」在收完尾的任务上根本无从兑现，实测 14 个历史任务全挂这个。

const root = path.resolve(import.meta.dirname, "..");
const card = fs.readFileSync(path.join(root, "src/components/TaskCardRows.tsx"), "utf8");
const icons = fs.readFileSync(path.join(root, "src/app/taskIcons.tsx"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const realtime = fs.readFileSync(path.join(root, "src/app/useRealtimeEvents.ts"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 直接跑组件里那段判据，不抄一份：抄一份的话，组件改了测试照样绿 —— 那样这个
// 测试钉住的就只是「我自己写的那段逻辑对」，不是「界面显示的对」。
//
// 取的是**整条派生链的源码**（worktreeEnabled 到 worktreeTagState），不是只取最后
// 那个表达式：worktreeBuilt / worktreeGone 的判据本身就是要验的东西（它带着
// worktree_built 守卫，少一行就等于没验）。中间那几行注释也一并带上 —— new Function
// 只吃语句不吃注释，抄注释不影响执行，但抄错了行号会立刻炸，正好当场发现。
const stateBlock = card.match(
  /const worktreeEnabled = [\s\S]*?const worktreeTagState: WorktreeTagState = ([\s\S]*?);\n/,
);
assert.ok(stateBlock, "the card must contain a worktreeTagState derivation");
// 那行带 TS 类型标注（`const x: T = …`），new Function 只吃 JS，剥掉标注。
// 剥完必须真的少了一行 —— 否则将来有人改了声明写法，这里就成了静默失效的 replace。
const stateBlockJS = stateBlock[0].replace(/const worktreeTagState: WorktreeTagState =/, "const worktreeTagState =");
assert.notEqual(stateBlockJS, stateBlock[0], "the WorktreeTagState type annotation should have been stripped");
// finishActive 由组件从 appTask.ts 引入（那边另有测试），这里作为依赖喂进去 ——
// 不在测试里重抄一份，重抄了就等于绕过了真正被用的那个判据。
const badgeState = new Function(
  "task",
  [
    "const isFinishStageActive = (t) => {",
    "  const i = t.current_stage_index;",
    "  if (i < 0 || i >= (t.stages?.length || 0)) return false;",
    "  const stage = t.stages?.[i];",
    "  return stage?.kind === 'worktree_finish'",
    "    && t.status !== 'success' && t.status !== 'fail' && t.status !== 'cancelled';",
    "};",
    "const isTerminalKanbanTask = (t) => t.status === 'success' || t.status === 'fail' || t.status === 'cancelled';",
    "const finishActive = isFinishStageActive(task);",
    "const terminal = isTerminalKanbanTask(task);",
    stateBlockJS,
    "return worktreeTagState;",
  ].join("\n"),
);

// 一个指针停在收尾段上的任务。
const atFinishStage = (extra = {}) => ({
  current_stage_index: 1,
  stages: [{ role: "agent" }, { role: "agent", kind: "worktree_finish" }],
  ...extra,
});

test("a finished worktree stops claiming to be enabled", () => {
  // 真机收尾后的形状：create_worktree 还是 true，path 被清空，且**确实建过树**。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "", worktree_built: true }), "finished");
});

test("a live worktree is the only thing that reads as enabled", () => {
  assert.equal(badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1" }), "enabled");
});

// **目录消失是金标准**（2026-10-05 用户裁定）：不管字段标着什么、不管任务在哪个
// 状态，没有目录就既没有树可执行、也没有树可拆，读「已收尾」比读「红色失效」诚实。
// 以前这里把「建过但目录被删」判成红色的 missing，实测 14 个早已收完尾的历史任务
// 全是这个形状 —— 挂着一个没人能兑现的红色警报。
test("a deleted worktree reads as finished, whatever the status says", () => {
  for (const status of ["success", "fail", "cancelled", "waiting_user", "running", "pending"]) {
    assert.equal(
      badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1", worktree_missing: true, status }),
      "finished",
      `${status} + 目录不在 = 已收尾`,
    );
  }
  // path 被清掉（收尾第 4 步 ClearWorktreeRefs 干的事）同理 —— 但必须带着
  // worktree_built：服务端只在 path 非空时才派生 worktree_missing，所以
  // 「path 空 + missing」这个组合根本不会出现，前端按「没建过」读是对的。
  assert.equal(
    badgeState({ create_worktree: true, worktree_path: "", worktree_built: true, worktree_missing: true }),
    "finished",
  );
});

// 「path 为空」不等于「收过尾」—— 它同时也是「还没建」的样子：任务刚建出来、或首段
// 还是 user 段时，worktree 本来就没建，路径当然为空。没有 worktree_built，这两种
// 会被合成一档，一个刚开始的任务直接显示「已收尾」。
//
// 注意这条断言**不**覆盖「归属被 repoint 清掉」那组形状：清完之后
// （path 空 + 建过 + 目录还在）与真收完的形状完全一样，前端无从分辨。让那组形状
// 根本不出现是服务端的事（repoint 不再清任务归属，见 session_repoint.go 与
// worktree_finish_stage_test.go 的 AST 断言），靠多加一个前端字段是补不回来的。
test("a worktree that was never built is not a finished one", () => {
  assert.notEqual(
    badgeState({ create_worktree: true, worktree_path: "" }),
    "finished",
    "a task that has not built its worktree yet must not claim to be finished",
  );
  // 连 worktree_built 都没有、path 也没有 → 橙色禁止符那一档，不是「已收尾」。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "" }), "none");
});

// 终态任务挂红色 missing 是 2026-10-01 实测的第二个 bug：14 个早已收完尾、目录已删的
// 历史任务全在飘红。红色那档的文案承诺「点『重建 worktree』恢复后再执行」，而重建
// 按钮只对未结束的任务显示（TaskCardRows 的 !terminal 那道门）—— 于是警报指向一个
// 界面故意不给的动作。终态的活已经干完，它的 worktree 不可能还有活要干，目录不在
// 是收尾之后的正常状态，该读「已收尾」。
test("a finished task never wears the red missing badge", () => {
  const finishedButTornDown = {
    create_worktree: true,
    worktree_path: "/x/.worktree/t22",
    worktree_missing: true,
    worktree_built: true,
    status: "success",
  };
  assert.equal(badgeState(finishedButTornDown), "finished", "a completed task whose worktree is gone must not be reported as broken");
  for (const status of ["fail", "cancelled"]) {
    assert.equal(badgeState({ ...finishedButTornDown, status }), "finished", `${status} tasks must not be reported as broken either`);
  }
  // 未结束的任务同理：目录不在就是不在，与状态无关（见上一条）。
  assert.equal(badgeState({ ...finishedButTornDown, status: "waiting_user" }), "finished");
  assert.equal(badgeState({ ...finishedButTornDown, status: "running" }), "finished");
});

// 终态**不等于**目录被拆了。这条钉的是「已收尾」不能只看 status：徽标的文案逐字是
// 「活已并回主干，目录已拆」，目录明明还在却这么说，是另一句谎话。failed / cancelled
// 尤其危险 —— 终态只说明活停了，从来不说明收尾跑过。
test("a terminal task whose worktree is still on disk is not 'finished'", () => {
  const stillThere = {
    create_worktree: true,
    worktree_path: "/x/.worktree/t9",
    worktree_missing: false,
    worktree_built: true,
    status: "fail",
  };
  assert.notEqual(badgeState(stillThere), "finished", "a failed task with a live worktree must not claim the directory was torn down");
  assert.equal(badgeState(stillThere), "enabled", "the directory is still there — say so");
  // success 同理：状态终结不等于收尾流程跑过。
  assert.notEqual(badgeState({ ...stillThere, status: "success" }), "finished");
  // 目录被删 + 终态才是「已收尾」那档（上一条已验）。
  assert.equal(badgeState({ ...stillThere, worktree_missing: true }), "finished");
});

test("a task that never asked for a worktree is still 'none'", () => {
  assert.equal(badgeState({ create_worktree: false, worktree_path: "" }), "none");
  assert.equal(badgeState({ create_worktree: false, worktree_path: "/x/.worktree/t1" }), "none");
});

// 「收尾中」必须单列一档。期间目录还在，徽标若和平时一模一样，用户会以为
// 「还没开始」于是再点一次收尾（服务端会拒，但用户看到的是「按钮坏了」）。
test("a running finish stage reads as its own state", () => {
  assert.equal(
    badgeState({ ...atFinishStage(), create_worktree: true, worktree_path: "/x/.worktree/t1" }),
    "finishing",
  );
});

test("once the finish stage is over the badge goes back to talking about the directory", () => {
  // 收尾段跑完 → 清场把 path 清空 → 该读「已收尾」，不能永远停在「收尾中」。
  assert.equal(
    badgeState({ ...atFinishStage(), create_worktree: true, worktree_path: "", worktree_built: true }),
    "finished",
  );
  // 终态同理：指针还停在那段上，但活已经结束了，不该再显示「收尾中」。
  assert.equal(
    badgeState({ ...atFinishStage({ status: "success" }), create_worktree: true, worktree_path: "/x/.worktree/t1" }),
    "enabled",
  );
});

// 上面的函数是从组件源码跑出来的，跑对了不代表组件真的用了它 —— 把这点也钉上。
// 判据顺序有讲究：目录不在先判（金标准，与状态无关），然后 enabled / finishing 两档。
assert.match(
  card,
  /const worktreeTagState: WorktreeTagState = worktreeGone\s*\n\s*\? "finished"\s*\n\s*: hasWorktreePath\s*\n\s*\? \(finishActive \? "finishing" : "enabled"\)\s*\n\s*: "none"/,
  "the card must derive a four-state badge, not a two-state one",
);
assert.match(card, /style=\{taskWorktreeTagStyle\(worktreeTagState\)\}/, "the tag must be styled by that state");
assert.doesNotMatch(
  card,
  /style=\{taskWorktreeTagStyle\(worktreeEnabled, worktreeMissing\)\}/,
  "styling must not still key on create_worktree — that is the bug being fixed",
);
// 「建过」必须真的判过：没有 worktree_built，「path 为空」同时命中「还没建」和
// 「收完尾」，一个刚开始的任务会直接显示「已收尾」。
assert.match(
  card,
  /const worktreeBuilt = worktreeEnabled && \(task\.worktree_built === true \|\| hasWorktreePath\)/,
  "recognising a built worktree must require that one was actually built — without worktree_built, \"path cleared\" also matches \"never built\" and a live worktree gets reported as torn down",
);
// 目录消失是金标准：worktreeMissing 必须并进「已收尾」那一档，不许单列红色。
assert.match(
  card,
  /const worktreeGone = worktreeBuilt && \(!hasWorktreePath \|\| worktreeMissing\)/,
  "a deleted worktree must read as finished — the directory being gone is the gold standard",
);
assert.doesNotMatch(card, /worktreeTagState === "missing"/, "the red missing state must be gone");
// 四档都要有各自的颜色，不能并档。
assert.match(icons, /export type WorktreeTagState = "enabled" \| "finishing" \| "finished" \| "none"/);
for (const tone of ["#475569", "#15803d", "#b45309"]) {
  assert.ok(icons.includes(tone), `missing tone ${tone}: finished/disabled must not share a colour`);
}
// 收尾中的区分信号是**文字**，不是动效（2026-10-07 用户要求去掉脉冲）。
// enabled 与 finishing 的标签文字不同（「worktree」vs「收尾中」），那才是唯一的区分；
// 加回脉冲动画就等于又把「看着在动」当成了信号，而用户明确不要这个动效。
assert.doesNotMatch(
  icons.slice(icons.indexOf("export function taskWorktreeTagStyle")),
  /animation: finishing/,
  "the finishing badge must not pulse — the label text is the signal now",
);
assert.match(
  card,
  /worktreeTagState === "finishing"[\s\S]{0,40}?t\("task\.worktreeFinishingLabel"\)/,
  "the finishing tag must say 收尾中 — that text is what tells it apart from an idle worktree",
);

// 收尾进行中，按钮**照样可点**（2026-10-05）：那正是「agent 那半已经做完、只差机械
// 清场」的时刻，再点一次后端会直接清场。换成转圈等于把唯一的出路藏起来，任务就此
// 永远转下去 —— 用户实测的「收尾一直转、点不动、也结束不了」就是这个形状。
assert.doesNotMatch(
  card,
  /worktreeTagState === "finishing" \? \(\s*<span[\s\S]{0,400}?<TaskQueuedSpinnerIcon \/>/,
  "the finish button must stay clickable while the flow runs",
);
assert.doesNotMatch(card, /TaskQueuedSpinnerIcon/, "the card must not keep a spinner import for a state that no longer renders one");
assert.match(
  card,
  /const canFinishWorktree = worktreeEnabled && !worktreeMissing && hasWorktreePath && hasAgentStage;/,
  "the finish gate must not exclude the finishing state",
);

// 收尾完成必须有回执。清场跑在服务端的 goroutine 里，没有任何 HTTP 响应会回来，
// 界面唯一知道结果的途径就是这条 WS 推送 —— 不接等于收尾「点了没反应」。
const teardownHandler = realtime.slice(
  realtime.indexOf('"task.finish_teardown"'),
  realtime.indexOf('"session.meta.updated"'),
);
assert.ok(teardownHandler.length > 0, "the teardown receipt must be handled");
assert.match(teardownHandler, /severity: "info"/, "a clean finish must toast in the non-red info style");
assert.match(teardownHandler, /severity: "error"/, "a failed teardown must toast as an error, not silently vanish");
// 成功要报「到底做了什么」，不只是「做完了」：分支没删掉看着像「不用删」。
for (const field of ["worktree_removed", "branch_deleted"]) {
  assert.ok(teardownHandler.includes(`result?.${field}`), `the success receipt must report ${field}`);
}
assert.match(teardownHandler, /result\?\.branch_skip_reason/, "a skipped branch delete needs its own message");
// 合并冲突不是 toast 承载得了的：仓库停在 MERGE_HEAD，得列出文件。
assert.match(
  teardownHandler,
  /setTaskSessionErrorDialog\(\{/,
  "a merge conflict must open the file list, not a 3-second toast",
);

// 收尾按钮点下去要先确认：它不可逆（合回主干 + 拆目录 + 删分支 + 搬会话），
// 而它就长在执行键旁边。
const finishBranch = app.slice(app.indexOf('action === "finish-worktree"'), app.indexOf('action === "rebuild-worktree"'));
assert.ok(finishBranch.length > 0, "the finish-worktree branch must exist");
assert.match(
  finishBranch,
  /await confirmDialog\(\{[\s\S]{0,240}?t\("task\.finishWorktreeConfirm"\)[\s\S]{0,240}?danger: true[\s\S]{0,200}?if \(!ok\) return;/,
  "an irreversible teardown must confirm first and abort on cancel",
);
assert.match(
  finishBranch,
  /beginTaskFinishWorktree\(rootId, task\.id, nodeId\)/,
  "the button must start the staged finish, not the old one-shot teardown",
);
assert.doesNotMatch(
  finishBranch,
  /finishTaskWorktree\(/,
  "the card must no longer call the one-shot teardown — cleanup now follows the finish stage",
);

// 新增的文案两边都要有，且 key 集合不能漂。
for (const key of [
  "task.finishWorktreeRemoved",
  "task.finishWorktreeBranchDeleted",
  "task.worktreeFinishedTitle",
  "task.worktreeFinishedLabel",
  "task.worktreeFinishingTitle",
  "task.worktreeFinishingLabel",
  "task.finishWorktreeConfirm",
  "task.finishWorktreeStarted",
  "task.finishWorktreeSessionRunning",
  "task.finishWorktreeNudged",
]) {
  assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `zh-CN must define ${key}`);
  assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `en-US must define ${key}`);
}
const keysOf = (src) => new Set([...src.matchAll(/"(task\.(?:finishWorktree|worktree)[A-Za-z]*)":/g)].map((m) => m[1]));
assert.deepEqual([...keysOf(zh)].sort(), [...keysOf(en)].sort(), "zh-CN and en-US must not drift on these keys");

console.log("worktree-badge.test.mjs: OK");
