import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

// worktree 徽标的五种状态 + 收尾流程的整条反馈链。
//
// 前四种是**真跑出来的**：真机点了一次收尾，worktree 目录确实被拆了、分支确实
// 没了，但徽标纹丝不动、界面一声不吭。原因分别是
//   ① 徽标只看 create_worktree（创建时的配置，永久 true），不看路径还在不在；
//   ② 成功分支只刷新数据就 return，只有「分支没删掉」才弹窗。
// 两者都是「功能其实做对了、界面没说出来」，测试要是只查源码形状照样会绿。
// 后两种（finishing、回执）是 2026-09 收尾改成流水线阶段之后补的：清场改跑在
// 服务端自己的 goroutine 里，界面唯一知道结果的途径只剩 WS 推送。

const root = path.resolve(import.meta.dirname, "..");
const card = fs.readFileSync(path.join(root, "src/components/TaskCardRows.tsx"), "utf8");
const icons = fs.readFileSync(path.join(root, "src/app/taskIcons.tsx"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const realtime = fs.readFileSync(path.join(root, "src/app/useRealtimeEvents.ts"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 直接跑组件里那段判据，不抄一份：抄一份的话，组件改了测试照样绿 —— 那样这个
// 测试钉住的就只是「我自己写的那段逻辑对」，不是「界面显示的对」。
// 取判据那一段的源码、用 new Function 真的执行它。
const stateExpr = card.match(
  /const worktreeTagState: WorktreeTagState = ([\s\S]*?);\n/,
);
assert.ok(stateExpr, "the card must contain a worktreeTagState derivation");
const badgeState = new Function(
  "task",
  [
    "const worktreeEnabled = task.create_worktree === true;",
    "const hasWorktreePath = worktreeEnabled && !!task.worktree_path;",
    "const worktreeMissing = worktreeEnabled && task.worktree_missing === true;",
    "const worktreeFinished = worktreeEnabled && !hasWorktreePath && !worktreeMissing;",
    // 收尾段在跑（isFinishStageActive 的判据；本测试只关心它怎么影响徽标）。
    "const index = task.current_stage_index;",
    "const stages = task.stages?.length || 0;",
    "const finishActive = index >= 0 && index < stages && task.stages[index]?.kind === 'worktree_finish'",
    "  && task.status !== 'success' && task.status !== 'fail' && task.status !== 'cancelled';",
    `return ${stateExpr[1]};`,
  ].join("\n"),
);

// 一个指针停在收尾段上的任务。
const atFinishStage = (extra = {}) => ({
  current_stage_index: 1,
  stages: [{ role: "agent" }, { role: "agent", kind: "worktree_finish" }],
  ...extra,
});

test("a finished worktree stops claiming to be enabled", () => {
  // 真机收尾后的形状：create_worktree 还是 true，path 被清空。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "" }), "finished");
});

test("a live worktree is the only thing that reads as enabled", () => {
  assert.equal(badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1" }), "enabled");
});

test("a deleted worktree and a finished one are not the same thing", () => {
  // 并成一档就是这次的 bug：用户分不清自己的 worktree 是「出过事」还是「干完了」。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1", worktree_missing: true }), "missing");
  assert.equal(badgeState({ create_worktree: true, worktree_path: "", worktree_missing: true }), "missing");
  assert.equal(badgeState({ create_worktree: true, worktree_path: "", worktree_missing: false }), "finished");
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
    badgeState({ ...atFinishStage(), create_worktree: true, worktree_path: "" }),
    "finished",
  );
  // 终态同理：指针还停在那段上，但活已经结束了，不该再显示「收尾中」。
  assert.equal(
    badgeState({ ...atFinishStage({ status: "success" }), create_worktree: true, worktree_path: "/x/.worktree/t1" }),
    "enabled",
  );
});

// 上面的函数是抄的，抄对了不代表组件真的用了它 —— 把这点也钉上。
assert.match(
  card,
  /const worktreeTagState: WorktreeTagState = worktreeMissing\s*\n\s*\? "missing"[\s\S]{0,200}?finishActive \? "finishing" : "enabled"/,  "the card must derive a five-state badge, not a two-state one",
);
assert.match(card, /style=\{taskWorktreeTagStyle\(worktreeTagState\)\}/, "the tag must be styled by that state");
assert.doesNotMatch(
  card,
  /style=\{taskWorktreeTagStyle\(worktreeEnabled, worktreeMissing\)\}/,
  "styling must not still key on create_worktree — that is the bug being fixed",
);
assert.match(
  card,
  /const worktreeFinished = worktreeEnabled && !hasWorktreePath && !worktreeMissing/,
  "the card must recognise a finished worktree (path cleared, directory gone)",
);
// 五档都要有各自的颜色，不能并档。
assert.match(icons, /export type WorktreeTagState = "enabled" \| "finishing" \| "missing" \| "finished" \| "none"/);
for (const tone of ["#b91c1c", "#475569", "#15803d", "#b45309"]) {
  assert.ok(icons.includes(tone), `missing tone ${tone}: finished/disabled must not share a colour`);
}
// 收尾中必须看得出「这玩意儿正在动」：动与不动是它和 enabled 唯一的区分信号。
assert.match(
  icons.slice(icons.indexOf("export function taskWorktreeTagStyle")),
  /animation: finishing \? "mindfs-task-ask-user-pulse/,
  "the finishing badge must pulse, otherwise it is pixel-identical to an idle worktree",
);
// 收尾进行中，那个按钮本身也该变成转圈而不是还能再点的键。
assert.match(
  card,
  /worktreeTagState === "finishing" \? \(\s*<span[\s\S]{0,400}?<TaskQueuedSpinnerIcon \/>/,
  "the finish button must turn into a spinner while the flow runs",
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
]) {
  assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `zh-CN must define ${key}`);
  assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `en-US must define ${key}`);
}
const keysOf = (src) => new Set([...src.matchAll(/"(task\.(?:finishWorktree|worktree)[A-Za-z]*)":/g)].map((m) => m[1]));
assert.deepEqual([...keysOf(zh)].sort(), [...keysOf(en)].sort(), "zh-CN and en-US must not drift on these keys");

console.log("worktree-badge.test.mjs: OK");
