import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

function createHookRuntime() {
  const states = [];
  const refs = [];
  const deps = [];
  const cleanups = [];
  let cursor = 0;
  const pendingEffects = [];
  const timers = new Map();
  let nextTimer = 1;
  const sameDeps = (left, right) =>
    Array.isArray(left) && Array.isArray(right) && left.length === right.length && left.every((value, index) => value === right[index]);
  const react = {
    useState(initial) {
      const index = cursor++;
      if (!(index in states)) states[index] = typeof initial === "function" ? initial() : initial;
      return [states[index], (next) => { states[index] = typeof next === "function" ? next(states[index]) : next; }];
    },
    useRef(initial) {
      const index = cursor++;
      if (!(index in refs)) refs[index] = { current: initial };
      return refs[index];
    },
    useMemo(factory) { cursor += 1; return factory(); },
    useEffect(effect, nextDeps) {
      const index = cursor++;
      if (!sameDeps(deps[index], nextDeps)) {
        if (cleanups[index]) cleanups[index]();
        deps[index] = nextDeps;
        pendingEffects.push(() => { cleanups[index] = effect(); });
      }
    },
  };
  return {
    react,
    window: {
      setTimeout(callback) { const id = nextTimer++; timers.set(id, callback); return id; },
      clearTimeout(id) { timers.delete(id); },
    },
    render(run) {
      cursor = 0;
      const result = run();
      while (pendingEffects.length) pendingEffects.shift()();
      return result;
    },
    flushTimers() { for (const [id, callback] of [...timers]) { timers.delete(id); callback(); } },
    cleanup() { cleanups.forEach((cleanup) => cleanup?.()); },
  };
}

function loadHook(runtime, dispatcher) {
  const source = readFileSync(new URL("../src/hooks/useSessionStream.ts", import.meta.url), "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {}, module: { exports: {} }, window: runtime.window,
    require: (id) => {
      if (id === "react") return runtime.react;
      if (id.includes("services/session")) return { sessionService: { dispatcher }, isPersistedSeq: (seq) => Number(seq || 0) > 0 };
      if (id.includes("i18n")) return { translateNow: (key) => `translated:${key}` };
      if (id.includes("sessionTimeline")) return {
        applySessionContextWindow: (timeline) => timeline,
        buildBaseTimeline: (exchanges, _aux, blocks) => {
          if (globalThis.__onBuild) globalThis.__onBuild(exchanges, blocks);
          const timeline = [];
          for (const ex of exchanges) {
            if (ex.role === "user") {
              timeline.push({ id: `user:${ex.seq}`, type: "user_text", content: ex.content || "" });
            } else if (ex.role === "agent" && ex.seq && blocks && blocks[String(ex.seq)]) {
              timeline.push(...blocks[String(ex.seq)].map((block) => ({
                id: `block:${ex.seq}:${block.pos}`,
                type: block.type === "reasoning" ? "thought" : "assistant_text",
                content: block.text || "",
              })));
            } else if (ex.role === "thought") {
              timeline.push({ id: ex.thought_id || "transient-thought", type: "thought", content: ex.content });
            }
          }
          return timeline;
        },
        dedupeToolCards: (timeline) => timeline,
        settleRunningTools: (timeline) => timeline,
      };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: "useSessionStream.ts" });
  return sandbox.exports.useSessionStream;
}

test("useSessionStream keeps streaming until authoritative session.done", () => {
  const runtime = createHookRuntime();
  let subscription;
  let unsubscribed = 0;
  const dispatcher = {
    isSessionStreaming: () => true,
    subscribe: (_key, handlers) => {
      subscription = handlers;
      return () => { unsubscribed += 1; };
    },
  };
  const useSessionStream = loadHook(runtime, dispatcher);
  const render = (pending = true) => runtime.render(() => useSessionStream("s1", [], {}, undefined, pending));

  render();
  assert.equal(render().isStreaming, true);
  subscription.onStream({ type: "message_done" });
  let result = render();
  assert.equal(result.isStreaming, true, "message_done is a message boundary, not a turn terminator");
  assert.equal(result.streamStatusText, "");

  subscription.onStream({ type: "tool_call_update", data: {} });
  assert.equal(render().isStreaming, true, "later auxiliary events do not revive a falsely completed stream");

  subscription.onDone();
  assert.equal(render().isStreaming, false, "session.done terminates the local stream");

  subscription.onStream({ type: "message_chunk", data: { content: "next" } });
  assert.equal(render(false).isStreaming, false, "authoritative pending reconciliation gates the exported state");
  runtime.flushTimers();
  runtime.cleanup();
  assert.equal(unsubscribed, 2, "dependency transition and unmount both unsubscribe");
});

test("thought stream updates do not rebuild persisted block timeline", () => {
  const runtime = createHookRuntime();
  let subscription;
  let buildCount = 0;
  globalThis.__onBuild = (exs, blocks) => { if (Object.keys(blocks || {}).length > 0) buildCount += 1; };
  const dispatcher = {
    isSessionStreaming: () => true,
    subscribe: (_key, handlers) => { subscription = handlers; return () => {}; },
  };
  globalThis.__bc = 0;
  const useSessionStream = loadHook(runtime, dispatcher, (exs, blocks) => {
    if (Object.keys(blocks || {}).length > 0) globalThis.__bc += 1;
  });
  let exchanges = [
    { role: "user", seq: 1, content: "prompt" },
    { role: "agent", seq: 1, content: "", timestamp: "t1" },
  ];
  const blocks = { "1": [
    { seq: 1, pos: 0, type: "reasoning", text: "stable reasoning" },
    { seq: 1, pos: 1, type: "text", text: "answer" },
  ] };
  const aux = {};
  const render = () => runtime.render(() => useSessionStream("s1", exchanges, aux, undefined, true, blocks));

  const initial = render();
  assert.deepEqual([...initial.timeline.map((item) => item.content)], ["prompt", "stable reasoning", "answer"]);
  assert.equal(buildCount, 1);

  // transient thought grows
  exchanges = [...exchanges, { role: "thought", thought_id: "live-thought", content: "thinking" }];
  subscription.onStream({ type: "thought_chunk", data: { content: "thinking", id: "live-thought" } });
  runtime.flushTimers();
  const afterThought = render();
  assert.equal(buildCount, 1, "thought stream change must not rebuild persisted block timeline");
  assert.deepEqual([...afterThought.timeline.map((item) => item.content)], ["prompt", "stable reasoning", "answer", "thinking"]);

  // blocks change -> rebuild
  const changedBlocks = { ...blocks, "1": [...blocks["1"], { seq: 1, pos: 2, type: "reasoning", text: "next stable" }] };
  runtime.render(() => useSessionStream("s1", exchanges, {}, undefined, true, changedBlocks));
  assert.equal(buildCount, 2, "new persisted block window must rebuild the blocks-backed base timeline");
});
