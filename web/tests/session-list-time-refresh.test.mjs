import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("session-list-time-refresh", () => {
// 右侧会话列表「完成后时间不动」的回归（2026-11-22，本机 llmux 项目）。
//
// 现场：llmux 会话 11:47 跑完（服务端 updated_at 已触摸），列表 11:52 仍显示开跑前的
// 「49m」，之后一直不动。根因不是时间格式，也不是服务端没写库：
//   · `session.done` / `session.stream` 只发给**挂载了该会话**的客户端
//     （StreamHub 按 sessionKey 绑客户端），后台项目 / 看板任务的会话收不到；
//   · 那时唯一的完成信号是 5s 轮询的 pending true→false 差分，而
//     `newlyCompletedPendingKeys` 刻意**排除激活节点**（激活节点假定 WS 已处理）；
//   · 于是本地节点上的后台会话跑完，只剩「灭灯」这一下会触发重渲染，
//     updated_at 仍是开跑前那条 → 卡片把旧时间重新算成「49m」，并且再无重拉。
//
// 修法：另出 `completedPendingKeys`（不排除任何节点）只用于触发列表重拉，
// 提示音/未读仍用排除激活节点的原差分，避免连响两遍。

function loadModule(relPath, exportNames, requireMap = {}) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      for (const [needle, mod] of Object.entries(requireMap)) {
        if (id.includes(needle)) return mod;
      }
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const scope = loadModule("src/shared/scope.ts", ["scopeSessionKey", "sessionKeyNodeId"]);
const pending = loadModule(
  "src/shared/pendingCompletion.ts",
  ["newlyCompletedPendingKeys", "completedPendingKeys"],
  { scope },
);

const { scopeSessionKey, sessionKeyNodeId } = scope;
const { newlyCompletedPendingKeys, completedPendingKeys } = pending;
assert.equal(typeof sessionKeyNodeId, "function");

// ── 复现：本地节点（激活节点）上的后台会话跑完 ──────────────────────────────
// 键的形状与生产一致：scopeSessionKey(nodeId, rootId, sessionKey)。
const activeNode = "local";
const doneKey = scopeSessionKey(activeNode, "llmux", "1795316462-0592cf467797");
const runningKey = scopeSessionKey(activeNode, "mindfs", "1795319535-91554826f24c");
const previous = { [doneKey]: true, [runningKey]: true };
const next = { [runningKey]: true };
const okNodes = new Set([activeNode]);

// 修前行为：激活节点被排除 → 一条完成都看不见（这正是「列表不重拉」的原因）。
assert.deepEqual(
  Array.from(newlyCompletedPendingKeys(previous, next, okNodes, activeNode)),
  [],
  "提示音/未读差分必须继续排除激活节点（WS 已处理，再判会连响）",
);

// 修后：不排除节点的差分必须看见它 —— 这就是触发列表重拉的信号。
assert.deepEqual(
  Array.from(completedPendingKeys(previous, next, okNodes)),
  [doneKey],
  "列表重拉差分必须包含激活节点上的后台会话",
);
assert.deepEqual(
  Array.from(completedPendingKeys(previous, next, okNodes, activeNode)),
  [],
  "completedPendingKeys 仍须支持显式排除（保持与旧差分同一语义）",
);

// ── 差分边界：别把「还在跑」「本轮没问到的节点」「历史无归属键」当成完成 ──
assert.deepEqual(
  Array.from(completedPendingKeys({ [runningKey]: true }, { [runningKey]: true }, okNodes)),
  [],
  "仍在 pending 的会话不能触发重拉",
);
assert.deepEqual(
  Array.from(
    completedPendingKeys(
      { [scopeSessionKey("fn", "llmux", "s1")]: true },
      {},
      new Set([activeNode]),
    ),
  ),
  [],
  "本轮没问到的节点（拉取失败/未启用）不能据此判完成",
);
assert.deepEqual(
  Array.from(completedPendingKeys({ "legacy-key": true }, {}, okNodes)),
  [],
  "归属不明的历史单节点键不能触发重拉",
);

// ── 接线守卫：轮询必须在完成时真的重拉列表 ────────────────────────────────
const stripComments = (source) =>
  source
    .split("\n")
    .filter((line) => {
      const trimmed = line.trim();
      return !trimmed.startsWith("//") && !trimmed.startsWith("*") && !trimmed.startsWith("/*");
    })
    .join("\n");

const pollSource = stripComments(fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/useReplyingSessionPoll.ts"),
  "utf8",
));
assert.match(pollSource, /completedPendingKeys\s*\(/, "轮询必须使用不排除激活节点的差分");
assert.match(
  pollSource,
  /completedAnyNode\.length\s*>\s*0[\s\S]{0,200}reloadListRef\.current\?\.\(\)/,
  "完成差分命中后必须调用列表重拉入口",
);
assert.match(
  pollSource,
  /const\s+reloadListRef\s*=\s*useRef\(/,
  "重拉入口必须走 ref，避免依赖变动重建 5s 轮询",
);

const reloadSource = stripComments(fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/useAppMultiProjectReload.ts"),
  "utf8",
));
assert.match(
  reloadSource,
  /useReplyingSessionPoll\(\{[\s\S]*scheduleMultiProjectSessionReload[\s\S]*\}\)/,
  "必须把带合并窗口的重拉入口交给轮询",
);
});
