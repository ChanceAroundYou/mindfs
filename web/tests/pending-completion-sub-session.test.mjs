import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("pending-completion-sub-session", () => {
// 子任务完成不要提醒：`alertableCompletedKeys` 把子会话从「该响提示音」里剔掉，
// `carrySubSessionKeys` 负责跨帧记住「哪些键是子会话」。
//
// 为什么必须跨帧：完成是从 pending 表的 true→false（或整条消失）差分出来的，
// 判完成的那一帧里会话已经不在 `sessions` 快照里，`subSession` 字段读不到。
// 只看当前快照 = 子会话标记永远命中不了，提示音照响。

function loadModule(relPath, exportNames, stubs = {}) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: (id) => stubs[id] || {} };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const { alertableCompletedKeys, carrySubSessionKeys } = loadModule(
  "src/shared/pendingCompletion.ts",
  ["alertableCompletedKeys", "carrySubSessionKeys"],
  { "./scope": { sessionKeyNodeId: () => "home" } },
);

// 子会话完成：不响，但仍是「完成」的一条（未读徽标照标）。
assert.deepEqual(
  [...alertableCompletedKeys(["home::go::child1"], ["home::go::child1"])],
  [],
  "子会话完成不能进提示音列表",
);
// 父会话完成：照响。
assert.deepEqual([...alertableCompletedKeys(["home::go::parent"], ["home::go::child1"])], ["home::go::parent"]);
// 一轮里父子同时完成：只留父。
assert.deepEqual(
  [...alertableCompletedKeys(["home::go::child1", "home::go::parent"], ["home::go::child1"])],
  ["home::go::parent"],
);
assert.deepEqual([...alertableCompletedKeys([], [])], []);

// 跨帧：本轮快照里标出的子会话，下一帧它已不在快照里但仍在 replying —— 标记必须还在。
const afterFirstFrame = carrySubSessionKeys([], ["home::go::child1"], { "home::go::child1": true });
assert.deepEqual([...afterFirstFrame], ["home::go::child1"]);
const afterGone = carrySubSessionKeys(afterFirstFrame, [], {});
assert.deepEqual([...afterGone], [], "会话完成后要忘掉标记，别无限累积");

// 节点请求失败时 pending 表保留旧值（仍为 true），标记不能因为本轮没拉到快照就丢。
const nodeFailedFrame = carrySubSessionKeys(new Set(["pc::go::child2"]), [], { "pc::go::child2": true });
assert.deepEqual([...nodeFailedFrame], ["pc::go::child2"]);

// 空键不许进表：空字符串会被当成「所有空键都是子会话」。
assert.deepEqual([...carrySubSessionKeys([], [""], {})], []);

// silent（工作流节点回合）与子会话同路：轮询侧必须把它记进同一张「完成不响」表。
const pollSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/useReplyingSessionPoll.ts"),
  "utf8",
);
assert.match(
  pollSource,
  /item\?\.subSession === true \|\| item\?\.silent === true/,
  "轮询把 silent 回合当普通完成 → 任务子步骤跑完仍会响提示音",
);

// WS 的 session.done 也必须认这个标记（后台推送被静默了，但前台页面的音效是另一条路径）。
const wsSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/realtimeWsHandlers.ts"),
  "utf8",
);
assert.match(
  wsSource,
  /const isSilentTurn = \(payload as any\)\?\.silent === true;[\s\S]{0,120}playNotificationSound\("complete"\)/,
  "session.done 路径必须跳过 silent 回合的提示音",
);
});
