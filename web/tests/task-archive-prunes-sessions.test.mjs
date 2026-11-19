// 任务归档 = 归档「这个任务名下的全部会话」。
//
// 症状（丢了会怎样）：用户在看板把一张终态任务归档，前端只重拉看板、不动会话列表；
// 后端虽然给会话写了 archived_at，侧栏却还挂着「看板归档 / #86」——用户看到的就是
// 「归档了任务，对应会话没归档」。
//
// 修法：后端任务归档级联广播 session.meta.updated，payload 带 archived_at；前端
// session.meta.updated 命中 archived_at 时把该会话（及其子会话，各自一条广播）从
// 缓存与主列表摘掉，不再走增量刷新（否则 archived_at 不改 updated_at，增量拉不回来
// 也删不掉，会话会一直留着）。
//
// 这里跑的是**真 handler**（transpile + vm 加载源码），直接喂一帧广播看 ref 与列表。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

const sourcePath = path.resolve(
  import.meta.dirname,
  "../src/app/realtimeWsSessionMeta.ts",
);

function loadFactory() {
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
    },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      if (id.includes("appSession"))
        return { normalizeMode: (v) => v, normalizeSessionAuxFlags: (v) => v };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports.createSessionMetaHandlers;
}

const createSessionMetaHandlers = loadFactory();

function makeCtx({ cachedSession, list }) {
  const state = {
    sessions: [...list],
    cacheVersion: 0,
    reloads: [],
    multiReloads: 0,
  };
  const ctx = {
    refs: {
      boundSessionByRootRef: { current: {} },
      selectedSessionRef: { current: null },
      sessionCacheRef: {
        current: cachedSession ? { "root::s1": cachedSession } : {},
      },
      sessionsRef: { current: state.sessions },
    },
    setters: {
      setDrawerSessionForRoot: () => {},
      setSelectedSession: () => {},
      setSessions: (fn) => {
        state.sessions = typeof fn === "function" ? fn(state.sessions) : fn;
        ctx.refs.sessionsRef.current = state.sessions;
      },
    },
    actions: {
      bumpCacheVersion: () => {
        state.cacheVersion += 1;
      },
      loadSessionsForRoot: (rootID, opts) => {
        state.reloads.push({ rootID, opts });
      },
      playNotificationSound: () => {},
      refreshTaskWorktree: () => {},
      refreshTasksForRelatedSession: () => {},
      scheduleMultiProjectSessionReload: () => {
        state.multiReloads += 1;
      },
      updateSessionRelatedWorktreeForKey: () => {},
    },
    values: {
      multiProjectSessionsEnabled: false,
      rootSessionKey: (rootID, key) => `${rootID}::${key}`,
      scopedRootKey: (rootID) => rootID,
    },
  };
  return { ctx, state };
}

const local = { refreshSessionRelatedFiles: () => {} };

function fireArchiveEvent(ctx, archivedAt) {
  const handlers = createSessionMetaHandlers(ctx, local);
  handlers["session.meta.updated"]({}, {
    root_id: "root",
    session: { key: "s1", archived_at: archivedAt },
  });
}

test("archived_at 命中：会话立刻从主列表与缓存里摘掉，且不再走增量刷新", () => {
  const { ctx, state } = makeCtx({
    cachedSession: { key: "s1", root_id: "root", exchanges: [] },
    list: [
      { key: "s1", root_id: "root", task_id: "task_1" },
      { key: "s2", root_id: "root" },
    ],
  });

  fireArchiveEvent(ctx, "2026-11-20T03:44:24Z");

  assert.deepEqual(
    state.sessions.map((item) => item.key),
    ["s2"],
    "被归档的会话必须从主列表里消失（否则侧栏原地不动）",
  );
  assert.equal(
    ctx.refs.sessionCacheRef.current["root::s1"],
    undefined,
    "缓存里也必须删掉，否则下一次缓存命中会把它放回来",
  );
  assert.equal(
    state.reloads.length,
    0,
    "命中归档必须提前 return：归档不改 updated_at，增量重拉既拉不回来也删不掉",
  );
  assert.ok(state.cacheVersion > 0, "删缓存后必须 bump，触发列表重渲染");
});

test("archived_at 为空：照常合并元数据并触发增量刷新（取消归档后会话回来）", () => {
  const { ctx, state } = makeCtx({
    cachedSession: { key: "s1", root_id: "root", name: "旧名", exchanges: [] },
    list: [{ key: "s1", root_id: "root", name: "旧名" }],
  });

  fireArchiveEvent(ctx, null);

  assert.equal(
    ctx.refs.sessionCacheRef.current["root::s1"].name,
    "旧名",
    "未归档帧不做摘除，只合并元数据",
  );
  assert.equal(state.sessions.length, 1, "未归档会话不能被过滤掉");
  assert.equal(state.reloads.length, 1, "未归档帧仍要触发一次列表刷新");
});
