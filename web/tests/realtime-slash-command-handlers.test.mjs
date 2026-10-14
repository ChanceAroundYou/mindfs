import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { MODULES } from "./source-map.mjs";
import { test } from "vitest";

test("realtime-slash-command-handlers", async () => {
const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
const realtime = read("src/app/useRealtimeEvents.ts");
const handlers = read("src/app/realtimeWsSlashCommandHandlers.ts");

assert.match(handlers, /export function createRealtimeWsSlashCommandHandlers/);
assert.match(handlers, /event\.type === "message_chunk"/);
assert.match(handlers, /event\.type === "login_notice"/);
assert.match(handlers, /event\.type === "error"/);
assert.match(handlers, /status: "complete"/);
assert.match(handlers, /current\.requestId !== requestId/);
assert.match(realtime, /\.\.\.slashCommandHandlers/);
assert.ok(
  MODULES["src/app/useRealtimeEvents.ts"].includes("src/app/realtimeWsSlashCommandHandlers.ts"),
  "the logical useRealtimeEvents source must include extracted slash command handlers",
);

console.log("realtime-slash-command-handlers.test.mjs: OK");
});
