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
      if (id.includes("services/session")) return { sessionService: { dispatcher } };
      if (id.includes("i18n")) return { translateNow: (key) => `translated:${key}` };
      if (id.includes("sessionTimeline")) return {
        applySessionContextWindow: (timeline) => timeline,
        buildBaseTimeline: (exchanges) => exchanges,
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
