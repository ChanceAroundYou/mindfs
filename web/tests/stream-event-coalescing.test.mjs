import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 一次挂载/重放会把成百上千条流式事件打进 handler。以前每条 chunk 各自
// `[...exchanges]` + 线性查找 + `content + chunk`，长回合是 O(E²)/O(L²)。
// 现在同一帧内的 chunk 先进缓冲，flush 时拼好再写一次缓存。
function loadStreamModule() {
  const source = readFileSync(
    new URL("../src/app/realtimeWsSessionStream.ts", import.meta.url),
    "utf8",
  );
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    setTimeout,
    clearTimeout,
    require: (id) => {
      if (id.includes("sessionListMerge")) return { mergeSessionItems: (items) => items };
      if (id.includes("sessionDuration")) return { streamEventTimestamp: (event, fallback) => event.timestamp || fallback };
      if (id.includes("net/error")) return { reportError: () => {} };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: "realtimeWsSessionStream.ts" });
  return sandbox.exports;
}

function createCtx(log, counters) {
  const ref = (current) => ({ current });
  return {
    refs: {
      currentRootIdRef: ref("root"),
      currentRootNodeIdRef: ref("home"),
      pendingBySessionRef: ref({}),
    },
    setters: {
      setCodexRateLimitsRefreshToken: () => {},
      setMultiProjectSessionGroups: (update) => update([]),
      setSessions: (update) => update([]),
    },
    actions: {
      appendAgentChunkForSession: (...args) => log.push(["agent", ...args]),
      appendCompactNoticeForSession: () => {},
      appendPlanUpdateForSession: () => {},
      appendThoughtChunkForSession: (...args) => log.push(["thought", ...args]),
      appendTodoUpdateForSession: () => {},
      appendToolCallForSession: (...args) => log.push(["tool", ...args]),
      attachContextWindowToLatestAssistant: () => {},
      bumpCacheVersion: () => { counters.bumps += 1; },
      getNodeIdForRoot: () => "home",
      markSessionPending: () => {},
      resolveRootForSessionKey: () => null,
    },
    values: {
      rootSessionKey: (root, key) => `home::${root}::${key}`,
      t: (key) => key,
    },
  };
}

function chunk(content, mindfsID = "turn-1") {
  return {
    root_id: "root",
    session_key: "s1",
    event: { type: "message_chunk", data: { content }, mindfs_id: mindfsID },
  };
}

test("同一帧内多条文本 chunk 合流成一次写入，文本逐字一致", () => {
  const mod = loadStreamModule();
  const log = [];
  const counters = { bumps: 0 };
  const handler = mod.createRealtimeWsSessionStreamHandler(createCtx(log, counters), {
    handleSessionStreamDone: () => {},
  });

  handler(chunk("你"));
  handler(chunk("好"));
  handler(chunk("吗"));

  assert.equal(log.length, 0, "chunk 不逐条写缓存");

  mod.flushPendingChunks();

  assert.equal(log.length, 1, "同一帧的 chunk 只写一次");
  const [, rootID, sessionKey, content] = log[0];
  assert.equal(rootID, "root");
  assert.equal(sessionKey, "s1");
  assert.equal(content, "你好吗", "合流后文本必须与逐条应用逐字一致");
  assert.equal(counters.bumps, 0, "合流写入走 append* 内部的 debounce，不再额外立即 bump");
});

test("不同 mindfs_id / thought_id 各自成组，不会被拼到一起", () => {
  const mod = loadStreamModule();
  const log = [];
  const handler = mod.createRealtimeWsSessionStreamHandler(createCtx(log, { bumps: 0 }), {
    handleSessionStreamDone: () => {},
  });

  handler(chunk("A", "turn-1"));
  handler(chunk("B", "turn-2"));
  handler({
    root_id: "root",
    session_key: "s1",
    event: { type: "thought_chunk", data: { content: "T", id: "th-1" }, mindfs_id: "turn-1" },
  });
  mod.flushPendingChunks();

  assert.deepEqual(
    log.map(([kind, , , content]) => [kind, content]),
    [["agent", "A"], ["agent", "B"], ["thought", "T"]],
  );
});

test("结构性事件先 flush，保证「文本→工具→文本」顺序", () => {
  const mod = loadStreamModule();
  const log = [];
  const counters = { bumps: 0 };
  const handler = mod.createRealtimeWsSessionStreamHandler(createCtx(log, counters), {
    handleSessionStreamDone: () => {},
  });

  handler(chunk("before"));
  handler({
    root_id: "root",
    session_key: "s1",
    event: { type: "tool_call", data: { callId: "c1", title: "Bash" }, mindfs_id: "turn-1" },
  });

  assert.deepEqual(log.map(([kind]) => kind), ["agent", "tool"], "工具事件前必须先落盘文本");
  assert.equal(log[0][3], "before");
  assert.equal(counters.bumps, 1, "新工具卡仍是即时 bump");

  handler(chunk("after"));
  mod.flushPendingChunks();
  assert.deepEqual(log.map(([kind]) => kind), ["agent", "tool", "agent"], "顺序必须是 文本→工具→文本");
  assert.equal(log[0][3], "before");
  assert.equal(log[2][3], "after");
});

test("tool_call_update 不再逐条立即 bump", () => {
  const mod = loadStreamModule();
  const log = [];
  const counters = { bumps: 0 };
  const handler = mod.createRealtimeWsSessionStreamHandler(createCtx(log, counters), {
    handleSessionStreamDone: () => {},
  });

  handler({
    root_id: "root",
    session_key: "s1",
    event: { type: "tool_call_update", data: { callId: "c1", content: "line" }, mindfs_id: "turn-1" },
  });
  handler({
    root_id: "root",
    session_key: "s1",
    event: { type: "tool_call_update", data: { callId: "c1", content: "line2" }, mindfs_id: "turn-1" },
  });

  assert.equal(counters.bumps, 0, "工具续传走 append* 内部的 debounce");
  assert.equal(log.length, 2, "工具续传自身仍按事件应用到缓存");
});