import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 排队样式必须在**非前台**状态下也消失（2026-10-25 用户实测）。
//
// 症状：一句排队的指令在后台轮到它了、甚至跑完了，界面上的「排队中」一直不消失。
//
// 根因不在前端的选择逻辑，而在**收敛通道只有一条**：队列帧只由
// BroadcastSessionQueueUpdated 即时发一次、发完即忘。后台标签页/被挂起的 PWA 错过
// 「出队」那一帧之后再挂上来，服务端原先在队列为空时直接 return，什么都不发 ——
// 客户端手里那份旧队列就再也没有东西能纠正它。
//
// 两侧各自钉一条，缺一不可：
//   ① 服务端：挂上会话必发权威队列状态（含空），见
//      server/internal/api/stream_hub_queue_replay_test.go；
//   ② 前端：收到空队列帧必须**真的清掉**本地队列，而不是「空载荷当作没这回事」。
// 这里跑的是真 handler，直接喂一帧空队列看 ref。

const sourcePath = path.resolve(import.meta.dirname, "../src/app/realtimeWsHandlers.ts");

function loadHandlers() {
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      if (id.includes("realtimeWsSessionMeta")) return { createSessionMetaHandlers: () => ({}) };
      if (id.includes("realtimeWsTaskHandlers")) return { createRealtimeWsTaskHandlers: () => ({}) };
      if (id.includes("realtimeWsUserMessage")) return { handleSessionUserMessage: () => {} };
      if (id.includes("realtimeWsSessionAccepted")) return { handleSessionAccepted: () => {} };
      if (id.includes("realtimeWsRootChanged")) return { handleRootChanged: () => {} };
      if (id.includes("rootNode")) return { setRootNodeMap: () => {} };
      if (id.includes("nodeRegistry")) return { applyNodesFromServer: () => {}, syncNodesFromServer: () => {} };
      if (id.includes("appMisc")) return { mapManagedRootsToEntries: () => [], normalizeUpdateState: (v) => v };
      if (id.includes("appPath")) return { rootNodeKey: (...parts) => parts.join("::") };
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      if (id.includes("appSession")) return { normalizeMode: (v) => v };
      if (id.includes("services/session")) return { dropTransientExchanges: (s) => s, isSamePendingEcho: () => false };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports.createRealtimeWsHandlers;
}

const createRealtimeWsHandlers = loadHandlers();

function makeCtx(overrides = {}) {
  const queuedMessagesBySessionRef = { current: {} };
  const queueFrozenBySessionRef = { current: {} };
  const optimisticDequeuedIdsRef = { current: {} };
  let queueVersion = 0;
  const ctx = {
    refs: {
      queuedMessagesBySessionRef,
      queueFrozenBySessionRef,
      optimisticDequeuedIdsRef,
      currentRootIdRef: { current: "proj" },
      ...overrides.refs,
    },
    setters: { setQueueVersion: (fn) => { queueVersion = typeof fn === "function" ? fn(queueVersion) : fn; } },
    actions: {},
    values: { rootSessionKey: (rootId, key) => `${rootId}::${key}` },
  };
  return { ctx, queuedMessagesBySessionRef, queueFrozenBySessionRef, getVersion: () => queueVersion };
}

const noop = () => {};
const local = {
  reloadSessionForReplay: noop,
  getReplayTargetsForRoot: () => [],
  replayTargetsForAllRoots: noop,
  refreshSessionRelatedFiles: noop,
  handleSessionStreamDone: () => true,
  handleSessionStream: noop,
  handleSlashCommandStream: noop,
  handleSlashCommandDone: noop,
  handleFileChanged: noop,
  handleFileChangedBatch: noop,
};

test("后台错过的排队状态由挂载时的空队列帧收敛掉", () => {
  const { ctx, queuedMessagesBySessionRef, queueFrozenBySessionRef, getVersion } = makeCtx();
  const handlers = createRealtimeWsHandlers(ctx, local);
  const handler = handlers["session.queue.updated"];
  assert.equal(typeof handler, "function", "session.queue.updated 必须注册处理器");

  const cacheKey = "proj::sess-1";
  // 错过「出队」帧之后客户端手里的旧状态：那条其实已经执行完的消息。
  queuedMessagesBySessionRef.current[cacheKey] = [
    { id: "q1", content: "已经跑完的排队指令" },
  ];
  queueFrozenBySessionRef.current[cacheKey] = true;
  const before = getVersion();

  // 服务端在 session.ready 时补发的权威状态：队列已空。
  handler({}, { root_id: "proj", session_key: "sess-1", queue: [], queue_frozen: false });

  assert.deepEqual(
    queuedMessagesBySessionRef.current[cacheKey],
    [],
    "空队列帧必须清掉本地队列 —— 否则排队样式永远不消失",
  );
  assert.equal(queueFrozenBySessionRef.current[cacheKey], false, "冻结标记同样以服务端为准");
  assert.ok(getVersion() > before, "必须触发一次重渲染（ActionBar 读的是 queueVersion）");
});

test("收敛与当前选中会话无关，非前台会话同样清空", () => {
  const { ctx, queuedMessagesBySessionRef } = makeCtx();
  const handler = createRealtimeWsHandlers(ctx, local)["session.queue.updated"];
  // 当前根仍是 proj，但这条帧属于**另一条**会话（后台在跑的那条）。
  queuedMessagesBySessionRef.current["proj::sess-bg"] = [{ id: "q9", content: "后台那条" }];
  handler({}, { root_id: "proj", session_key: "sess-bg", queue: [] });
  assert.deepEqual(queuedMessagesBySessionRef.current["proj::sess-bg"], []);
});

test("空队列帧不得抹掉用户刚点「立即发送」的乐观出队保护失效后的语义", () => {
  const { ctx, queuedMessagesBySessionRef, queueFrozenBySessionRef } = makeCtx();
  const handlers = createRealtimeWsHandlers(ctx, local);
  const cacheKey = "proj::sess-1";
  // 服务端仍有两条：一条刚被乐观隐藏，一条还在排队。
  queuedMessagesBySessionRef.current[cacheKey] = [{ id: "q1", content: "还在排队" }];
  ctx.refs.optimisticDequeuedIdsRef.current[cacheKey] = new Set(["q2"]);
  handlers["session.queue.updated"]({}, {
    root_id: "proj",
    session_key: "sess-1",
    queue: [
      { id: "q1", content: "还在排队" },
      { id: "q2", content: "刚点了立即发送" },
    ],
    queue_frozen: false,
  });
  assert.deepEqual(
    queuedMessagesBySessionRef.current[cacheKey].map((item) => item.id),
    ["q1"],
    "乐观出队的条目不能被服务端旧帧重新塞回来",
  );
  assert.equal(queueFrozenBySessionRef.current[cacheKey], false);
});
