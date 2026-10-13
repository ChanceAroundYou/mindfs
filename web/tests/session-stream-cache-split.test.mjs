import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const parent = readPhysical("src/app/useSessionStreamCache.ts");
const runtime = readFileSync(new URL("../src/app/useSessionStreamRuntimeMeta.ts", import.meta.url), "utf8");
const types = readFileSync(new URL("../src/app/sessionStreamCacheTypes.ts", import.meta.url), "utf8");

assert.ok(parent.split("\n").length - 1 < 500, "the physical cache coordinator stays below the frontend hard line");
assert.match(parent, /export type \{ SessionRuntimeMeta \} from "\.\/sessionStreamCacheTypes";/, "the old metadata type export remains compatible");
assert.match(parent, /useSessionStreamRuntimeMeta\(/);
assert.match(parent, /appendThoughtChunkForSession[\s\S]*?appendToolCallForSession[\s\S]*?appendTodoUpdateForSession[\s\S]*?appendPlanUpdateForSession[\s\S]*?appendCompactNoticeForSession[\s\S]*?attachContextWindowToLatestAssistant/, "non-agent merge rules and context-window updates remain in the coordinator");
assert.match(runtime, /const upsertSessionCache = useCallback/);
assert.match(runtime, /const resolveRuntimeMetaForSession = useCallback/);
assert.match(runtime, /const appendAgentChunkForSession = useCallback/);
assert.match(runtime, /hasSameMindFSID\(last, mindfsID\)/, "agent chunks still merge only for the same MindFS event identity");
assert.match(types, /export type SessionRuntimeMeta = \{/);

console.log("session-stream-cache-split.test.mjs: OK");
