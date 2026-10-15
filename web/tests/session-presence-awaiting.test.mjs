import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// C（"问完就完成、灯灭；答完又亮"）的针对性回归。
//
// AskUserQuestion 把一个逻辑任务切成两轮：提问轮 done → 灯灭（看着像任务结束），
// 答完起新轮 → 灯又亮。修向 (b)：turn 语义不动，用**空心灯**表达"在等你回答"，
// 与"子代理运行中"同形态但文案不同。
//
// 数据来源是会话列表项上的 awaiting_answer（服务端在 ask_user 未回答时下发）——
// 刻意不走 pending 真相表，进了那里就是实心灯，表达的恰好是相反的意思。

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

test("awaiting-presence: getSessionPresence 四态优先级", () => {
  const { getSessionPresence } = loadPresenceModule();
  // 自己在跑最优先（实心）。
  assert.equal(getSessionPresence(true, false, true), "running", "自跑优先于等回答");
  // 等回答 > 委托中 > 全闲（三档空心/灭的先后）。
  assert.equal(getSessionPresence(false, false, true), "awaiting");
  assert.equal(getSessionPresence(false, true, true), "awaiting", "自己在等回答时，后代在跑不改变本行的语义");
  assert.equal(getSessionPresence(false, true, false), "delegating");
  assert.equal(getSessionPresence(false, false, false), "idle");
});

test("awaiting-presence: 列表项上的 awaiting_answer 直接变成 awaiting 态", () => {
  const { computePresenceByKey, applyPresenceToItems } = loadPresenceModule();
  const items = [
    { key: "asked", pending: false, awaiting_answer: true },
    { key: "running", pending: true, awaiting_answer: false },
    { key: "idle", pending: false },
    // 有子代理在跑、自己也在等回答：本行说的是"等你回答"。
    { key: "both", pending: false, awaiting_answer: true },
    { key: "child", parent_session_key: "both", pending: true },
  ];
  const table = computePresenceByKey(items);
  assert.equal(table.asked, "awaiting");
  assert.equal(table.running, "running");
  assert.equal(table.idle, "idle");
  assert.equal(table.both, "awaiting");

  const stamped = applyPresenceToItems(items);
  assert.equal(stamped.find((i) => i.key === "asked").presence, "awaiting");
});

test("awaiting-presence: presenceFromTree 从同级列表读出本项的 awaiting", () => {
  const { presenceFromTree } = loadPresenceModule();
  const items = [
    { key: "p", pending: false, awaiting_answer: true },
    { key: "c1", parent_session_key: "p", pending: false },
  ];
  assert.equal(presenceFromTree("p", false, items), "awaiting");
  // 后代在跑时仍是"等回答"（本行的状态由本行决定）。
  const withChild = [
    { key: "p", pending: false, awaiting_answer: true },
    { key: "c1", parent_session_key: "p", pending: true },
  ];
  assert.equal(presenceFromTree("p", false, withChild), "awaiting");
  // 没有 awaiting_answer 的项不受影响。
  assert.equal(presenceFromTree("p", false, [{ key: "p", pending: false }]), "idle");
});

test("awaiting-presence: i18n 中英 key 必须同步存在", () => {
  const zh = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/i18n/locales/zh-CN-sessions.ts"),
    "utf8",
  );
  const en = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/i18n/locales/en-US-sessions.ts"),
    "utf8",
  );
  for (const key of ["session.awaitingAnswer", "sessionList.awaitingAnswer"]) {
    assert.ok(zh.includes(key), `zh-CN-sessions must contain ${key}`);
    assert.ok(en.includes(key), `en-US-sessions must contain ${key}`);
  }
});

test("awaiting-presence: 列表灯位必须有 awaiting 空心分支", () => {
  const card = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionCard.tsx"),
    "utf8",
  );
  assert.match(card, /sessionList\.awaitingAnswer/, "SessionCard hollow branch must use the awaiting label");
  assert.match(card, /awaiting/, "SessionCard must handle the awaiting presence");
});

test("awaiting-presence: viewer 头部与底部指示器都要有 awaiting 形态", () => {
  const viewer = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewer.tsx"),
    "utf8",
  );
  assert.match(viewer, /isAwaitingAnswer=/, "SessionViewer must pass awaiting state down");
  const header = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewerHeader.tsx"),
    "utf8",
  );
  assert.match(header, /session\.awaitingAnswer/, "header dot needs the awaiting label");
  const indicator = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/components/session/SessionViewerWaitingIndicator.tsx"),
    "utf8",
  );
  assert.match(indicator, /session\.awaitingAnswer/, "bottom indicator needs the awaiting copy");
  // 原有两档文案不能被吞掉。
  assert.match(indicator, /session\.subagentRunning/, "delegating copy must survive");
  assert.match(indicator, /session\.sentWaiting/, "sent-waiting copy must survive");
});

test("awaiting-presence: awaiting 不得塞进 pending 真相表", () => {
  const poll = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useReplyingSessionPoll.ts"),
    "utf8",
  );
  assert.doesNotMatch(
    poll,
    /awaiting/i,
    "等待回答不是 pending：进了这张表就是实心灯，与空心语义相反（来源是会话列表项）",
  );
});
