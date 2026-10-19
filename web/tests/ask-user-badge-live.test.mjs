import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("ask-user-badge-live", async () => {
// 问号徽标（会话行 aux_flags.ask_user_waiting）实时亮灭（G-AD）。
//
// 症状（2026-10-20 用户实测）：回答完徽标不立刻灭，要等这一轮会话结束才更新。
// 三条通道各自负责一段：
//   1. 后端：aux 写入后广播 session.meta.updated（同节点毫秒级）—— Go 侧守卫在
//      server/internal/api/session_aux_flags_broadcast_test.go；
//   2. 前端：session.meta.updated 就地更新列表项的 aux_flags（aux 变化**不改**
//      updated_at，按 updated_at 增量拉的列表请求带不回这条会话 —— 不就地打补丁
//      徽标会一直拿着旧值）；
//   3. 跨节点：5s 轮询 /api/replying-sessions 的 awaiting_answer 当真相，
//      合并进分组时同步覆盖 aux_flags.ask_user_waiting。

function loadModule(relPath, exportNames, requireStub = () => ({})) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: requireStub };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const { scopeSessionKey } = loadModule("src/shared/scope.ts", ["scopeSessionKey"]);

const { applyPendingToMultiProjectGroups } = loadModule(
  "src/app/appSession.ts",
  ["applyPendingToMultiProjectGroups"],
  (id) => {
    if (id.includes("scope")) return { sessionKeyNodeId: () => "", scopeSessionKey };
    if (id.includes("appTask")) return { normalizeFastService: (v) => v };
    return {};
  },
);

// ── 1. 轮询合并：键出现 = 知道真相（不在等）⇒ 徽标立刻灭 ──────────────
const groups = [
  {
    rootId: "proj",
    _nodeId: "pc",
    sessions: [
      {
        key: "s1",
        // 库里还写着「在等回答」—— 答题后应由轮询的真相覆盖掉
        aux_flags: { ask_user_waiting: true, has_plan: false, has_todos: false, has_task: false },
      },
      {
        key: "s2",
        // 这条会话**没**出现在轮询结果里（节点没问到 / 不在活跃列表）⇒ 不知情
        aux_flags: { ask_user_waiting: true, has_plan: false, has_todos: false, has_task: false },
      },
    ],
  },
];
const patched = applyPendingToMultiProjectGroups(
  groups,
  {},
  { [scopeSessionKey("pc", "proj", "s1")]: false },
);
const s1 = patched[0].sessions[0];
const s2 = patched[0].sessions[1];
assert.equal(s1.aux_flags.ask_user_waiting, false, "轮询说不在等 ⇒ 徽标必须立刻灭");
assert.equal(s1.awaiting_answer, false);
assert.equal(
  s2.aux_flags.ask_user_waiting,
  true,
  "轮询里缺席的会话 = 不知情，不能拿它清徽标（否则节点拉失败会误清一片）",
);
assert.equal(s2.awaiting_answer, false);

// 没有 awaitingByKey（老调用方）时不得动 aux_flags
const untouched = applyPendingToMultiProjectGroups(groups, {});
assert.equal(untouched[0].sessions[0].aux_flags.ask_user_waiting, true);

// ── 2. 源码守卫：session.meta.updated 必须就地更新列表项 ─────────────
//
// 症状「答完不灭，要会话结束才更新」在同节点也一样复现：aux 变化不改
// updated_at，按 updated_at 增量拉的列表请求**带不回**这条会话，
// 列表项会一直拿着旧值直到下一次全量刷新。
const metaSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/realtimeWsSessionMeta.ts"),
  "utf8",
);
assert.match(
  metaSrc,
  /setSessions\(\(prev\) =>/,
  "session.meta.updated 必须就地更新列表项（aux_flags）",
);
assert.match(metaSrc, /aux_flags: auxFlags/);
assert.match(
  metaSrc,
  /payload\.session\.aux_flags !== undefined/,
  "只有 payload 带 aux_flags 时才覆盖（缺省沿用缓存）",
);

// ── 3. 源码守卫：轮询必须记**显式 false** ────────────────────────────
//
// 「缺席 = 不在等」会把没被轮询到的会话的徽标误清；「出现即显式 false」
// 才能既区分不知情、又能立刻灭。
const pollSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/useReplyingSessionPoll.ts"),
  "utf8",
);
assert.match(
  pollSrc,
  /freshAwaiting\[scoped\] =\s*\n?\s*item\?\.awaiting_answer === true \|\| item\?\.awaitingAnswer === true;/,
  "轮询必须把每条出现过的会话记成显式 true/false",
);
assert.match(pollSrc, /mergeReplyingStateByNode\(multiProjectAwaitingRef\.current, freshAwaiting, okNodeIds\)/);

console.log("ask-user-badge-live.test.mjs: OK");
});
