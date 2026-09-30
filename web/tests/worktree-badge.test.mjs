import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

// worktree 徽标的四种状态 + 收尾成功要有回执。
//
// 这两条都是**真跑出来的**：真机点了一次收尾，worktree 目录确实被拆了、分支确实
// 没了，但徽标纹丝不动、界面一声不吭。原因分别是
//   ① 徽标只看 create_worktree（创建时的配置，永久 true），不看路径还在不在；
//   ② 成功分支只刷新数据就 return，只有「分支没删掉」才弹窗。
// 两者都是「功能其实做对了、界面没说出来」，测试要是只查源码形状照样会绿。

const root = path.resolve(import.meta.dirname, "..");
const card = fs.readFileSync(path.join(root, "src/components/TaskCardRows.tsx"), "utf8");
const icons = fs.readFileSync(path.join(root, "src/app/taskIcons.tsx"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 直接跑组件里那段判据，不抄一份：抄一份的话，组件改了测试照样绿 —— 那样这个
// 测试钉住的就只是「我自己写的那段逻辑对」，不是「界面显示的对」。
// 取判据那一段的源码、用 new Function 真的执行它。
const stateExpr = card.match(
  /const worktreeTagState = ([\s\S]*?);\n/,
);
assert.ok(stateExpr, "the card must contain a worktreeTagState derivation");
const badgeState = new Function(
  "task",
  [
    "const worktreeEnabled = task.create_worktree === true;",
    "const hasWorktreePath = worktreeEnabled && !!task.worktree_path;",
    "const worktreeMissing = worktreeEnabled && task.worktree_missing === true;",
    "const worktreeFinished = worktreeEnabled && !hasWorktreePath && !worktreeMissing;",
    `return ${stateExpr[1]};`,
  ].join("\n"),
);

test("a finished worktree stops claiming to be enabled", () => {
  // 真机收尾后的形状：create_worktree 还是 true，path 被清空。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "" }), "finished");
});

test("a live worktree is the only thing that reads as enabled", () => {
  assert.equal(badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1" }), "enabled");
});

test("a deleted worktree and a finished one are not the same thing", () => {
  // 并成一档就是本次的 bug：用户分不清自己的 worktree 是「出过事」还是「干完了」。
  assert.equal(badgeState({ create_worktree: true, worktree_path: "/x/.worktree/t1", worktree_missing: true }), "missing");
  assert.equal(badgeState({ create_worktree: true, worktree_path: "", worktree_missing: true }), "missing");
  assert.equal(badgeState({ create_worktree: true, worktree_path: "", worktree_missing: false }), "finished");
});

test("a task that never asked for a worktree is still 'none'", () => {
  assert.equal(badgeState({ create_worktree: false, worktree_path: "" }), "none");
  assert.equal(badgeState({ create_worktree: false, worktree_path: "/x/.worktree/t1" }), "none");
});

// 上面的函数是抄的，抄对了不代表组件真的用了它 —— 把这点也钉上。
assert.match(
  card,
  /const worktreeTagState = worktreeMissing \? "missing" : worktreeFinished \? "finished"/,
  "the card must derive a four-state badge, not a two-state one",
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
// 四档都要有各自的颜色，不能三档并两档。
assert.match(icons, /export type WorktreeTagState = "enabled" \| "missing" \| "finished" \| "none"/);
for (const tone of ["#b91c1c", "#475569", "#15803d", "#b45309"]) {
  assert.ok(icons.includes(tone), `missing tone ${tone}: finished/disabled must not share a colour`);
}

// 成功必须有回执。i18n key 早就写好了却从没用过 —— 这正是它没被发现的原因。
const finishBranch = app.slice(app.indexOf('action === "finish-worktree"'), app.indexOf('action === "rebuild-worktree"'));
assert.ok(finishBranch.length > 0, "the finish-worktree branch must exist");
assert.match(
  finishBranch,
  /setTaskSessionErrorDialog\(\{\s*title: t\("task\.finishWorktreeDone"\)/,
  "a successful finish must tell the user it worked",
);
for (const field of ["result.worktree_removed", "result.branch_deleted"]) {
  assert.ok(finishBranch.includes(field), `the success receipt must report ${field}`);
}
// 分支没删掉要单独说，别混进成功里。
assert.match(finishBranch, /!result\.branch_deleted && result\.branch_skip_reason/, "a skipped branch delete needs its own message");

// 新增的文案两边都要有，且 key 集合不能漂。
for (const key of [
  "task.finishWorktreeRemoved",
  "task.finishWorktreeBranchDeleted",
  "task.worktreeFinishedTitle",
  "task.worktreeFinishedLabel",
]) {
  assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `zh-CN must define ${key}`);
  assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `en-US must define ${key}`);
}
const keysOf = (src) => new Set([...src.matchAll(/"(task\.(?:finishWorktree|worktree)[A-Za-z]*)":/g)].map((m) => m[1]));
assert.deepEqual([...keysOf(zh)].sort(), [...keysOf(en)].sort(), "zh-CN and en-US must not drift on these keys");

console.log("worktree-badge.test.mjs: OK");