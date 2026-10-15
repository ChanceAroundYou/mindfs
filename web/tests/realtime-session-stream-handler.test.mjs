import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { MODULES } from "./source-map.mjs";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

function loadStreamHandler() {
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
    require: (id) => {
      if (id.includes("sessionListMerge")) return { mergeSessionItems: (items) => items };
      if (id.includes("sessionDuration")) return { streamEventTimestamp: (event, fallback) => event.timestamp || fallback };
      if (id.includes("net/error")) return { reportError: () => {} };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: "realtimeWsSessionStream.ts" });
  return sandbox.exports.createRealtimeWsSessionStreamHandler;
}

function loadAcceptedHandler() {
  const source = readFileSync(
    new URL("../src/app/realtimeWsSessionAccepted.ts", import.meta.url),
    "utf8",
  );
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    console,
    require: (id) => {
      if (id.includes("services/session")) return {
        isSamePendingEcho: (exchange, pending) => {
          if (!pending || exchange?.pending_ack !== true) return false;
          return exchange.content === pending.message && exchange.timestamp === pending.timestamp;
        },
      };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: "realtimeWsSessionAccepted.ts" });
  return sandbox.exports.handleSessionAccepted;
}

function createStreamContext(calls, shared) {
  const ref = (current) => ({ current });
  const ctx = {
    refs: {
      currentRootIdRef: ref("root"),
      currentRootNodeIdRef: ref("home"),
      pendingBySessionRef: shared.pendingBySessionRef,
      sessionCacheRef: shared.sessionCacheRef,
    },
    setters: {
      setCodexRateLimitsRefreshToken: (update) => update(0),
      setMultiProjectSessionGroups: (update) => update([]),
      setSessions: (update) => update([]),
    },
    actions: {
      appendAgentChunkForSession: (...args) => calls.append.push(args),
      appendCompactNoticeForSession: () => {},
      appendPlanUpdateForSession: () => {},
      appendThoughtChunkForSession: () => {},
      appendTodoUpdateForSession: () => {},
      appendToolCallForSession: () => {},
      attachContextWindowToLatestAssistant: () => {},
      bumpCacheVersion: () => { calls.bumps += 1; },
      getNodeIdForRoot: () => "home",
      markSessionPending: (...args) => calls.mark.push(args),
      resolveRootForSessionKey: () => null,
    },
    values: {
      rootSessionKey: (root, key) => `home::${root}::${key}`,
      t: (key) => key,
    },
  };
  return { calls, ctx };
}

function createAcceptedContext(acceptedCalls, shared, pendingRequest) {
  const ref = (current) => ({ current });
  const ctx = {
    refs: {
      currentRootNodeIdRef: ref("home"),
      pendingRequestRef: ref(pendingRequest),
      pendingBySessionRef: shared.pendingBySessionRef,
      pendingDraftRef: shared.pendingDraftRef,
      sessionCacheRef: shared.sessionCacheRef,
      drawerSessionByRootRef: shared.drawerSessionByRootRef,
    },
    setters: {
      setMultiProjectSessionPending: (...args) => acceptedCalls.pending.push(args),
      setDrawerSessionForRoot: (...args) => acceptedCalls.drawer.push(args),
    },
    actions: {
      promotePendingSessionForRoot: (...args) => acceptedCalls.promote.push(args),
      bumpCacheVersion: () => { acceptedCalls.bumps += 1; },
    },
    values: {
      rootSessionKey: (root, key) => `home::${root}::${key}`,
      scopedRootKey: (root) => `home::${root}`,
    },
  };
  return ctx;
}

test("realtime-session-stream-handler", async () => {
  const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
  const realtime = read("src/app/useRealtimeEvents.ts");
  const stream = read("src/app/realtimeWsSessionStream.ts");
  const streamHook = read("src/hooks/useSessionStream.ts");

  assert.match(stream, /export function createRealtimeWsSessionStreamHandler/);
  assert.match(stream, /Apply client-only activity before filtering stream content to the active node/);
  assert.match(stream, /if \(wsNid && curNid && wsNid !== curNid\) return/);
  assert.match(stream, /case "message_chunk"/);
  assert.match(stream, /case "thought_chunk"/);
  assert.match(stream, /case "compact_notice"/);
  assert.match(stream, /attachContextWindowToLatestAssistant/);
  assert.match(stream, /handleSessionStreamDone\(activeRoot, streamKey\)/);
  assert.doesNotMatch(stream, /pendingBySessionRef\.current\[ck\] = draft/);
  assert.doesNotMatch(stream, /\[session\/stream\] promote_pending/);
  assert.doesNotMatch(stream, /setBoundSessionForRoot\(activeRoot, streamKey\)/);
  assert.match(
    streamHook,
    /if \(event\.type === "message_done"\) \{\s*\/\/.*?\s*setStreamStatusText\(""\);\s*return;/s,
  );
  assert.match(realtime, /createRealtimeWsSessionStreamHandler\(ctx, \{/);
  assert.ok(MODULES["src/app/useRealtimeEvents.ts"].includes("src/app/realtimeWsSessionStream.ts"));

  const createHandler = loadStreamHandler();
  const ref = (current) => ({ current });
  const shared = { pendingBySessionRef: ref({}), sessionCacheRef: ref({}) };
  const calls = { append: [], mark: [], done: [], bumps: 0 };
  const { ctx } = createStreamContext(calls, shared);
  const handler = createHandler(ctx, {
    handleSessionStreamDone: (...args) => calls.done.push(args),
  });

  // A child/unknown stream is allowed to populate only its own cache route; it
  // cannot consume a user draft because no request_id correlation exists here.
  handler({
    root_id: "root",
    session_key: "opaque-child-key",
    event: { type: "message_chunk", data: { content: "child text" }, mindfs_id: "child-turn" },
  });
  assert.deepEqual(calls.append, [["root", "opaque-child-key", "child text", undefined, undefined, "child-turn"]]);
  assert.deepEqual(calls.mark, [["root", "opaque-child-key"]]);
  assert.equal(shared.pendingBySessionRef.current["home::root::opaque-child-key"], undefined);
  assert.equal(calls.bumps, 0, "chunks retain the existing debounced cache bump path");

  // A foreign-node event may update activity before filtering, but cannot claim
  // pending state or append visible content for this node.
  handler({
    root_id: "root",
    session_key: "foreign-child",
    _nodeId: "pc",
    event: { type: "message_chunk", data: { content: "must not append" } },
  });
  assert.equal(calls.append.length, 1);
  assert.equal(calls.mark.length, 1);

  // Positive route: a matching session.accepted (request_id correlated) promotes
  // only the accepted parent; a later child stream stays isolated.
  const handleSessionAccepted = loadAcceptedHandler();
  const acceptedCalls = { pending: [], drawer: [], promote: [], bumps: 0 };
  const now = new Date().toISOString();
  const draftPending = {
    rootId: "root",
    mode: "chat",
    agent: "claude",
    model: "m",
    agentMode: "",
    effort: "",
    fastService: "",
    shell: "",
    message: "hello",
    timestamp: now,
    requestId: "msg-1",
    tempKey: "pending-1",
  };
  const acceptedShared = {
    pendingBySessionRef: shared.pendingBySessionRef,
    pendingDraftRef: ref({ ...draftPending, requestId: "msg-1" }),
    sessionCacheRef: shared.sessionCacheRef,
    drawerSessionByRootRef: ref({}),
  };
  const acceptedCtx = createAcceptedContext(
    acceptedCalls,
    acceptedShared,
    { "msg-1": draftPending },
  );
  handleSessionAccepted(acceptedCtx, {}, {
    request_id: "msg-1",
    root_id: "root",
    session_key: "s-real",
    timestamp: now,
  });
  assert.equal(
    acceptedShared.pendingBySessionRef.current["home::root::s-real"]?.requestId,
    "msg-1",
    "accepted parent gets the request-correlated pending entry",
  );
  assert.deepEqual(
    acceptedCalls.promote[0].slice(0, 3),
    ["root", "pending-1", "s-real"],
    "accepted parent promotes temp key to the real session key",
  );

  // The child stream arriving after the accepted parent still cannot claim it.
  handler({
    root_id: "root",
    session_key: "opaque-child-key",
    event: { type: "message_chunk", data: { content: "more child" }, mindfs_id: "child-turn-2" },
  });
  assert.equal(shared.pendingBySessionRef.current["home::root::opaque-child-key"], undefined);
  assert.deepEqual(calls.mark[calls.mark.length - 1], ["root", "opaque-child-key"]);
  const lastAppend = calls.append[calls.append.length - 1];
  assert.equal(lastAppend[1], "opaque-child-key");

  // An unmatched accepted (unknown request_id) stays non-owning.
  const strayCalls = { pending: [], drawer: [], promote: [], bumps: 0 };
  const strayCtx = createAcceptedContext(strayCalls, {
    pendingBySessionRef: ref({}),
    pendingDraftRef: ref(null),
    sessionCacheRef: ref({}),
    drawerSessionByRootRef: ref({}),
  }, {});
  handleSessionAccepted(strayCtx, {}, {
    request_id: "unknown-req",
    root_id: "root",
    session_key: "s-stray",
  });
  assert.equal(strayCalls.promote.length, 0, "unmatched accepted must not promote");

  console.log("realtime-session-stream-handler.test.mjs: OK");
});
