import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// RED：委托态纯函数 + 后代求交 + i18n/灯位源码契约。
// getSessionPresence 缺失、i18n key 缺失、灯位无 delegating 分支时全部失败。

function loadPresenceModule() {
  const sourcePath = path.resolve(import.meta.dirname, "..", "src/shared/sessionPresence.ts");
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: () => ({}) };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

test("session-presence: getSessionPresence 三态优先级", () => {
  const { getSessionPresence } = loadPresenceModule();
  assert.equal(typeof getSessionPresence, "function", "sessionPresence must export getSessionPresence");
  // 自跑优先：自己在跑时即使后代也在跑，仍是 running（实心）。
  assert.equal(getSessionPresence(true, true), "running");
  assert.equal(getSessionPresence(true, false), "running");
  // 委托：自己闲 + 任一后代在跑 → delegating（空心）。
  assert.equal(getSessionPresence(false, true), "delegating");
  // 全闲 → idle（灭）。
  assert.equal(getSessionPresence(false, false), "idle");
});

test("session-presence: 后代 pending 经 collectSessionSubtreeKeys 求交", () => {
  const treePath = path.resolve(import.meta.dirname, "..", "src/shared/sessionTree.ts");
  const treeSource = fs.readFileSync(treePath, "utf8");
  const treeCompiled = ts.transpileModule(treeSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const treeSandbox = { exports: {}, module: { exports: {} } };
  vm.runInNewContext(treeCompiled, treeSandbox, { filename: treePath });
  const { collectSessionSubtreeKeys } = treeSandbox.exports;

  const scopePath = path.resolve(import.meta.dirname, "..", "src/shared/scope.ts");
  const scopeSource = fs.readFileSync(scopePath, "utf8");
  const scopeCompiled = ts.transpileModule(scopeSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const scopeSandbox = { exports: {}, module: { exports: {} }, require: () => ({}) };
  vm.runInNewContext(scopeCompiled, scopeSandbox, { filename: scopePath });
  const { scopeSessionKey } = scopeSandbox.exports;

  const { getSessionPresence, hasRunningDescendant } = loadPresenceModule();
  assert.equal(typeof hasRunningDescendant, "function", "sessionPresence must export hasRunningDescendant");

  // 父 p + 子 c1 + 孙 g1（fork 不应被收进来）。
  const items = [
    { key: "p" },
    { key: "c1", parent_session_key: "p" },
    { key: "g1", parent_session_key: "c1" },
    { key: "f1", parent_session_key: "p", fork_from: "p" },
  ];
  void collectSessionSubtreeKeys;
  void scopeSessionKey;
  void getSessionPresence;
  // 子在跑、父自己闲 → 后代命中 → delegating。
  const pendingByKey = { "n1::r1::c1": true };
  const keyOf = (rootId, sessionKey) => `n1::${rootId}::${sessionKey}`;
  assert.equal(hasRunningDescendant("p", items, pendingByKey, keyOf, "r1"), true);
  // 全闲 → 无后代命中。
  assert.equal(hasRunningDescendant("p", items, {}, keyOf, "r1"), false);
  // 自己在跑不算后代：后代表里只有自己时仍是 false（自跑由 selfPending 表达）。
  assert.equal(hasRunningDescendant("p", [{ key: "p" }], { "n1::r1::p": true }, keyOf, "r1"), false);
});

test("session-presence: i18n 中英 key 必须同步存在", () => {
  const zh = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/i18n/locales/zh-CN-sessions.ts"),
    "utf8",
  );
  const en = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/i18n/locales/en-US-sessions.ts"),
    "utf8",
  );
  for (const key of ["session.subagentRunning", "sessionList.delegating"]) {
    assert.ok(zh.includes(key), `zh-CN-sessions must contain ${key}`);
    assert.ok(en.includes(key), `en-US-sessions must contain ${key}`);
  }
});

test("session-presence: computePresenceByKey 一次遍历算全表", () => {
  const { computePresenceByKey } = loadPresenceModule();
  assert.equal(typeof computePresenceByKey, "function", "sessionPresence must export computePresenceByKey");
  const items = [
    { key: "p", pending: false },
    { key: "c1", parent_session_key: "p", pending: false },
    { key: "g1", parent_session_key: "c1", pending: true },
    { key: "x", pending: true },
    { key: "f1", parent_session_key: "p", pending: true, fork_from: "p" },
  ];
  const table = computePresenceByKey(items);
  assert.equal(table.p, "delegating", "祖先经由孙辈命中委托");
  assert.equal(table.c1, "delegating", "中间层同样空心");
  assert.equal(table.g1, "running", "自己在跑是实心");
  assert.equal(table.x, "running");
  assert.equal(table.f1, "running", "fork 项自己的 pending 仍由自己表达");
  void table;
});

test("session-presence: 列表项 presence 由列表一次算好盖上", () => {
  const composition = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useAppViewComposition.ts"),
    "utf8",
  );
  assert.match(composition, /applyPresenceToItems/, "presence must be stamped once per list, not per card");
  assert.doesNotMatch(
    composition,
    /sessions\.map\([\s\S]{0,400}presenceFromTree/,
    "per-card derivation in a list map is the O(n^2) shape we are avoiding",
  );
  // 单项目列表的 pending 也必须来自真相表（轮询只写分组，项上的 pending 会过期）。
  assert.match(composition, /multiProjectPendingByKey\[rootSessionKey\(root, key\)\]/, "single-list pending must be projected from the truth table");
});

test("session-presence: 列表灯位必须有空心 delegating 分支", () => {
  const card = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionCard.tsx"),
    "utf8",
  );
  assert.match(card, /delegating/, "SessionCard must render a delegating (hollow) branch");
  assert.match(card, /sessionList\.delegating/, "SessionCard hollow branch must use sessionList.delegating label");
});

test("session-presence: presenceFromTree 由同级列表直接算三态", () => {
  const { presenceFromTree } = loadPresenceModule();
  assert.equal(typeof presenceFromTree, "function", "sessionPresence must export presenceFromTree");
  const items = [
    { key: "p", pending: false },
    { key: "c1", parent_session_key: "p", pending: true },
  ];
  // 自己闲 + 子在跑 → delegating。
  assert.equal(presenceFromTree("p", false, items), "delegating");
  // 自己也在跑 → running（自跑优先）。
  assert.equal(presenceFromTree("p", true, items), "running");
  // 谁都不在跑 → idle。
  assert.equal(presenceFromTree("p", false, [{ key: "p", pending: false }]), "idle");
  // 只剩后代项没有自己 → 后代仍算。
  assert.equal(presenceFromTree("p", false, [{ key: "c1", parent_session_key: "p", pending: true }]), "delegating");
});

test("session-presence: 列表灯位必须消费真实 presence，不得挂假字段", () => {
  const card = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionCard.tsx"),
    "utf8",
  );
  assert.doesNotMatch(card, /__delegating/, "SessionCard must not branch on a never-assigned placeholder field");
  // 灯位状态由 useSessionCard 计算后经 props 传入（view 层不做数据推导）。
  assert.match(card, /presence/, "SessionCard must render from the computed presence value");
});

test("session-presence: 委托分支不得吞掉原有生成/等待文案", () => {
  const indicator = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewerWaitingIndicator.tsx"),
    "utf8",
  );
  assert.match(indicator, /session\.generating/, "streaming copy must survive");
  assert.match(indicator, /session\.sentWaiting/, "sent-waiting copy must survive");
  assert.match(indicator, /session\.subagentRunning/, "delegating copy required");
});

test("session-presence: viewer 把委托态传给底部指示器与头部", () => {
  const viewer = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewer.tsx"),
    "utf8",
  );
  assert.match(viewer, /isDelegating=/, "SessionViewer must pass delegating state to the indicator");
  assert.match(viewer, /SessionViewerHeader[\s\S]{0,300}isDelegating/, "viewer header must receive delegating state");
  const header = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewerHeader.tsx"),
    "utf8",
  );
  assert.match(header, /isDelegating/, "SessionViewerHeader must render a delegating dot");
  assert.match(header, /sessionList\.delegating|session\.subagentRunning/, "header dot needs its label");
});

test("session-presence: 选中会话的委托态由快照层从真相表算出", () => {
  const snapshots = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useAppSessionSnapshots.ts"),
    "utf8",
  );
  assert.match(snapshots, /presenceFromTree|computePresenceByKey/, "selected-session presence must be derived in the snapshot layer");
});

test("session-presence: viewer 等待指示器必须有常驻委托分支", () => {
  const indicator = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewerWaitingIndicator.tsx"),
    "utf8",
  );
  assert.match(indicator, /delegating/, "SessionViewerWaitingIndicator must handle delegating");
  assert.match(indicator, /session\.subagentRunning/, "delegating copy must use session.subagentRunning");
});
