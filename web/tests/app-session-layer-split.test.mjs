import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");

test("session aggregation delegates cohesive core and multi-project reload hooks below the hard line", () => {
  const layer = read("src/app/useAppSessionLayer.ts");
  const params = read("src/app/useAppSessionLayerTypes.ts");
  const core = read("src/app/useAppSessionCore.ts");
  const reload = read("src/app/useAppMultiProjectReload.ts");
  const app = read("src/App.tsx");

  assert.ok(layer.split("\n").length - 1 < 500, "session layer must stay below the 500-line hard line");
  assert.match(layer, /useAppSessionCore\(params\)/);
  assert.match(params, /export type UseAppSessionLayerParams =/);
  assert.match(core, /useSessionLifecycle\(/);
  assert.match(core, /useSessionMetadata\(/);
  assert.match(core, /useMultiProjectSessions\(/);
  assert.match(core, /useSessionStreamCache\(/);
  assert.match(core, /loadMultiProjectSessionGroups, loadMoreMultiProjectSessions/);
  assert.match(reload, /MULTI_PROJECT_RELOAD_DEBOUNCE_MS/);
  assert.match(reload, /useReplyingSessionPoll\(/);
  assert.ok(layer.indexOf("useAppSessionReload(") < layer.indexOf("useAppMultiProjectReload({"));
  assert.match(app, /useAppSessionLayer\(\{ \.\.\.l1 \}\)/);
});
