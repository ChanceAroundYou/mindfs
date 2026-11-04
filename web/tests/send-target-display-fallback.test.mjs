// 发送目标必须以「用户看到的那条会话」兜底。
//
// 2026-11 实测症状：在 fn 上用 codex 续聊，每条消息都开新会话。根因是发送目标
// 只认内存里的 boundSessionByRootRef，这张表一旦丢（刷新后未重建、作用域键随
// 节点漂移），发送路径就静默新建会话 —— 而主面板的转录还在屏幕上。
import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "vitest";

import { prepareSendMessageTarget } from "../src/app/prepareSendMessageTarget.ts";

const root = "CMAI";
const node = "fn-node";
const scopeKeyFn = (rootId) => `${node}::${rootId}`;
const rootSessionKey = (rootId, key) => `${node}::${rootId}::${key}`;

function makeParams(overrides = {}) {
  return {
    currentRootIdRef: { current: root },
    selectedSessionRef: { current: null },
    boundSessionByRootRef: { current: {} },
    sessionCacheRef: { current: {} },
    currentSessionRef: { current: null },
    pendingPlanMode: false,
    resolvePendingForSession: () => false,
    scopedRootKey: scopeKeyFn,
    rootSessionKey,
    updateSessionAgentForKey: () => {},
    setBoundSessionForRoot: () => {},
    setDrawerSessionForRoot: () => {},
    interactionModeRef: { current: "main" },
    ...overrides,
  };
}

const input = {
  message: "继续",
  mode: "chat",
  agent: "codex",
  newSessionName: "新会话",
};

let warns = [];
const originalWarn = console.warn;
beforeEach(() => {
  warns = [];
  console.warn = (...args) => warns.push(args);
});
afterEach(() => {
  console.warn = originalWarn;
});

test("绑定丢失时续接主面板显示的会话，并回写绑定", () => {
  const rebound = [];
  const displayed = { key: "sess-a", session_key: "sess-a", root_id: root, agent: "codex" };
  const target = prepareSendMessageTarget(
    makeParams({
      selectedSessionRef: { current: displayed },
      setBoundSessionForRoot: (rootId, key) => rebound.push([rootId, key]),
    }),
    input,
  );

  assert.equal(target.sendSessionKey, "sess-a");
  assert.equal(target.session?.key, "sess-a");
  assert.deepEqual(rebound, [[root, "sess-a"]], "兜底必须把绑定回写，否则下一条消息又丢");
  assert.ok(
    warns.some((args) => String(args[0]).includes("bound_missing fallback_displayed")),
    "走兜底必须留可查痕迹",
  );
});

test("绑定指向缓存里没有的会话时，也回落到显示中的那条", () => {
  const target = prepareSendMessageTarget(
    makeParams({
      boundSessionByRootRef: { current: { [scopeKeyFn(root)]: "sess-gone" } },
      selectedSessionRef: { current: { key: "sess-b", root_id: root } },
      sessionCacheRef: { current: {} },
    }),
    input,
  );
  assert.equal(target.sendSessionKey, "sess-b");
});

test("绑定正常时行为不变（缓存命中即用，不回落）", () => {
  const cached = { key: "sess-c", root_id: root, agent: "codex" };
  const target = prepareSendMessageTarget(
    makeParams({
      boundSessionByRootRef: { current: { [scopeKeyFn(root)]: "sess-c" } },
      selectedSessionRef: { current: { key: "sess-other", root_id: root } },
      sessionCacheRef: { current: { [rootSessionKey(root, "sess-c")]: cached } },
    }),
    input,
  );
  assert.equal(target.sendSessionKey, "sess-c");
  assert.equal(warns.length, 0);
});

test("没有绑定也没有显示会话时照旧新建（临时键仍是 pending-）", () => {
  const bound = [];
  const target = prepareSendMessageTarget(
    makeParams({ setBoundSessionForRoot: (rootId, key) => bound.push([rootId, key]) }),
    input,
  );
  assert.equal(target.sendSessionKey, undefined);
  assert.match(target.session.key, /^pending-/);
  assert.equal(bound.length, 1);
  assert.equal(bound[0][1], target.session.key);
});

test("别的根的显示会话不会被借用", () => {
  const target = prepareSendMessageTarget(
    makeParams({ selectedSessionRef: { current: { key: "other", root_id: "OTHER" } } }),
    input,
  );
  assert.equal(target.sendSessionKey, undefined);
  assert.match(target.session.key, /^pending-/);
});

test("抽屉持有输入时不劫持主面板会话（空抽屉语义仍是开新会话）", () => {
  const target = prepareSendMessageTarget(
    makeParams({
      interactionModeRef: { current: "drawer" },
      selectedSessionRef: { current: { key: "sess-a", root_id: root } },
    }),
    input,
  );
  assert.equal(target.sendSessionKey, undefined);
  assert.match(target.session.key, /^pending-/);
});

test("草稿还没拿到真键时明确阻断，而不是静默丢弃或再开一条", () => {
  const bound = [];
  const target = prepareSendMessageTarget(
    makeParams({
      boundSessionByRootRef: { current: { [scopeKeyFn(root)]: "pending-1700000000000" } },
      selectedSessionRef: {
        current: { key: "pending-1700000000000", root_id: root },
      },
      setBoundSessionForRoot: (rootId, key) => bound.push([rootId, key]),
    }),
    input,
  );
  assert.equal(target.blocked, "draft_key_missing");
  assert.equal(target.rootId, root);
  assert.equal(bound.length, 0, "阻断时不能再写一条新草稿绑定");
});
