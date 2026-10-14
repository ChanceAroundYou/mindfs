import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("session-connection-outbound-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const manager = readPhysical("src/services/session/sessionConnectionManager.ts");
const outbound = readPhysical("src/services/session/sessionConnectionOutbound.ts");

assert.ok(manager.split("\n").length - 1 < 500, "the connection manager stays below the frontend hard line");
assert.match(manager, /new SessionConnectionOutbound\(/);
for (const method of ["sendMessage", "setPlanMode", "runSlashCommand", "cancelMessage", "removeQueuedMessage", "updateQueuedMessage", "sendQueuedMessageNow", "answerQuestion", "markSessionReady"]) {
  assert.match(manager, new RegExp(`${method}\\(\.\.\.args: Parameters<SessionConnectionOutbound\\["${method}"\\]>`));
}
assert.match(manager, /private resendPendingMessages\(\)/, "the connection owner retains reconnect resend timing");
assert.match(manager, /this\.pendingMessages\.delete\(requestId\)/, "ack still clears the shared pending map");
assert.match(outbound, /if \(!this\.isSocketOpen\(\)\)[\s\S]*?compactContext/);
assert.match(outbound, /this\.pendingMessages\.set\(requestId, \{ id: requestId, message: msg \}\)/);
assert.match(outbound, /buildQueueRemoveMessage/);
assert.match(outbound, /buildSessionReadyMessage/);

console.log("session-connection-outbound-split.test.mjs: OK");
});
