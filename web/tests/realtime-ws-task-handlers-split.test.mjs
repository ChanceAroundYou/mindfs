import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("realtime-ws-task-handlers-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const parent = readPhysical("src/app/realtimeWsHandlers.ts");
const tasks = readFileSync(new URL("../src/app/realtimeWsTaskHandlers.ts", import.meta.url), "utf8");

assert.ok(parent.split("\n").length - 1 < 500, "the websocket handler factory stays below the frontend hard line");
assert.match(parent, /\.\.\.createRealtimeWsTaskHandlers\(ctx\)/);
assert.ok(parent.indexOf('"session.user_message"') < parent.indexOf("createRealtimeWsTaskHandlers(ctx)"));
assert.ok(parent.indexOf("createRealtimeWsTaskHandlers(ctx)") < parent.indexOf('"file.changed.batch"'), "task events keep their original registration position");
for (const event of ["task.updated", "task.finish_teardown", "task.deleted"]) {
  assert.match(tasks, new RegExp(`"${event}"`), `${event} remains registered`);
}
assert.match(tasks, /payloadNid && curNid && payloadNid !== curNid/);
assert.match(tasks, /refreshWorkspaceBoard\(\)/);
assert.match(tasks, /pruneTaskDetails\(/);

console.log("realtime-ws-task-handlers-split.test.mjs: OK");
});
