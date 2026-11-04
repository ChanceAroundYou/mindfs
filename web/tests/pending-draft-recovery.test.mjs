// 断线自愈：孤儿草稿（已发出、没拿到真键）在重连时用原 requestId 重放一次。
//
// 症状链：`session.accepted` 丢 → 绑定停在 `pending-*` → 发送路径把它当成
// 「没有绑定」→ 之后每条消息都新开一条会话。重放是安全的，因为服务端对
// 同一个 request_id 只回键、不重复跑回合（clientRequestRecord）。
import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "vitest";

import {
  MAX_RECOVERY_ATTEMPTS,
  collectOrphanedDrafts,
  recoverOrphanedDrafts,
  resetRecoveryAttempts,
} from "../src/app/pendingDraftRecovery.ts";

const root = "CMAI";
const tempKey = "pending-1700000000000";
const scopedRootKey = (rootId) => `fn-node::${rootId}`;

function draft(overrides = {}) {
  return {
    rootId: root,
    mode: "chat",
    agent: "codex",
    message: "继续",
    timestamp: "2026-11-04T00:00:00Z",
    requestId: "msg-1",
    tempKey,
    ...overrides,
  };
}

function params(pending = [], bound = tempKey, send = async () => true) {
  return {
    pendingRequestRef: { current: Object.fromEntries(pending.map((d) => [d.requestId, d])) },
    boundSessionByRootRef: {
      current: { [scopedRootKey(root)]: bound },
    },
    scopedRootKey,
    send,
  };
}

let warns = [];
const originalWarn = console.warn;
beforeEach(() => {
  warns = [];
  console.warn = (...args) => warns.push(args);
  resetRecoveryAttempts();
});
afterEach(() => {
  console.warn = originalWarn;
});

test("只挑「无真键、有临时键、且仍是该根绑定」的草稿", () => {
  const orphan = draft();
  const alreadyBound = draft({ requestId: "msg-2", sessionKey: "sess-a" });
  const noTemp = draft({ requestId: "msg-3", tempKey: "" });
  const notBound = draft({ requestId: "msg-4", tempKey: "pending-other" });
  const collected = collectOrphanedDrafts(
    params([orphan, alreadyBound, noTemp, notBound]),
  );
  assert.deepEqual(collected.map((d) => d.requestId), ["msg-1"]);
});

test("用户已经切到别的会话时不再插手", () => {
  const collected = collectOrphanedDrafts(params([draft()], "sess-b"));
  assert.equal(collected.length, 0);
});

test("重放用原 requestId 与原上下文，成功的不算放弃", async () => {
  const seen = [];
  const d = draft({ context: { currentRoot: root } });
  const result = await recoverOrphanedDrafts(
    params([d], tempKey, async (payload) => {
      seen.push(payload);
      return true;
    }),
  );
  assert.equal(seen.length, 1);
  assert.equal(seen[0].requestId, "msg-1");
  assert.deepEqual(seen[0].context, { currentRoot: root });
  assert.equal(result.retried.length, 1);
  assert.equal(result.gaveUp.length, 0);
  assert.ok(warns.some((a) => String(a[0]).includes("draft_recovery replay")));
});

test("发送失败不计入 retried，也不吞异常", async () => {
  const result = await recoverOrphanedDrafts(
    params([draft()], tempKey, async () => {
      throw new Error("boom");
    }),
  );
  assert.equal(result.retried.length, 0);
  assert.equal(result.gaveUp.length, 0);
});

test("连续断线最多重放 MAX_RECOVERY_ATTEMPTS 次，之后交给用户", async () => {
  let sends = 0;
  const p = params([draft()], tempKey, async () => {
    sends += 1;
    return true;
  });
  for (let i = 0; i < MAX_RECOVERY_ATTEMPTS; i += 1) {
    assert.equal((await recoverOrphanedDrafts(p)).retried.length, 1);
  }
  const last = await recoverOrphanedDrafts(p);
  assert.equal(last.retried.length, 0);
  assert.equal(last.gaveUp.length, 1);
  assert.equal(sends, MAX_RECOVERY_ATTEMPTS, "放弃之后不能再发");
});
