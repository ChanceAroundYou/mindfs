import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("session-pane-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const shell = readPhysical("src/components/app/SessionPane.tsx");
const main = readPhysical("src/components/app/SessionMainContent.tsx");
const trust = readPhysical("src/components/app/PendingPluginTrustView.tsx");
const types = readPhysical("src/components/app/sessionPaneTypes.ts");

assert.ok(shell.split("\n").length - 1 < 500);
assert.ok(main.split("\n").length - 1 < 500, "the content coordinator also stays below the hard line");
assert.match(shell, /export type \{ SessionPaneProps \} from "\.\/sessionPaneTypes";/);
assert.match(shell, /display: showSessionPane \? "flex" : "none"/);
assert.match(shell, /display: showSessionPane \? "none" : "flex"/);
assert.match(shell, /<SessionMainContent \{\.\.\.props\} \/>/);
assert.match(main, /mainView === "chat" && !selectedSession/);
assert.match(main, /activePendingPluginTrust/);
assert.match(main, /<PendingPluginTrustView/);
assert.match(trust, /onDisable/);
assert.match(trust, /onTrust/);
assert.match(types, /export interface SessionPaneProps/);

console.log("session-pane-split.test.mjs: OK");
});
