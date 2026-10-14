import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { MODULES } from "./source-map.mjs";
import { test } from "vitest";

test("realtime-session-stream-handler", async () => {
const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
const realtime = read("src/app/useRealtimeEvents.ts");
const stream = read("src/app/realtimeWsSessionStream.ts");

assert.match(stream, /export function createRealtimeWsSessionStreamHandler/);
assert.match(stream, /Apply client-only activity before filtering stream content to the active node/);
assert.match(stream, /if \(wsNid && curNid && wsNid !== curNid\) return/);
assert.match(stream, /case "message_chunk"/);
assert.match(stream, /case "thought_chunk"/);
assert.match(stream, /case "compact_notice"/);
assert.match(stream, /attachContextWindowToLatestAssistant/);
assert.match(stream, /handleSessionStreamDone\(activeRoot, streamKey\)/);
assert.match(realtime, /createRealtimeWsSessionStreamHandler\(ctx, \{/);
assert.ok(MODULES["src/app/useRealtimeEvents.ts"].includes("src/app/realtimeWsSessionStream.ts"));

console.log("realtime-session-stream-handler.test.mjs: OK");
});
