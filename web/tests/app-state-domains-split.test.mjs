import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");
const readPhysical = (path) => {
  const fd = openSync(resolve(root, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};

test("state layer delegates session, responsive, root-scope, and task-template domains below the hard line", () => {
  const layer = readPhysical("src/app/useAppStateLayer.ts");
  const sessions = read("src/app/useAppSessionListState.ts");
  const selection = read("src/app/useAppSessionSelectionState.ts");
  const responsive = read("src/app/useAppResponsiveState.ts");
  const rootScope = read("src/app/useAppRootScopeState.ts");
  const taskTemplates = read("src/app/useAppTaskTemplateState.ts");

  assert.ok(layer.split("\n").length - 1 < 500, "state layer must be below the 500-line hard line");
  assert.match(layer, /useAppSessionListState\(\)[\s\S]*?useTaskWorkspaceState\(\)[\s\S]*?useAppSessionSelectionState\(\)[\s\S]*?useAppResponsiveState\(\)/);
  assert.match(layer, /useAppRootScopeState\(\)[\s\S]*?useAppTaskTemplateState\([\s\S]*?\)[\s\S]*?const \[mainView/);
  assert.match(sessions, /useAppSessionPins\(\)/);
  assert.match(sessions, /multiProjectSessionsEnabled: true = true[\s\S]*?MULTI_PROJECT_RELOAD_DEBOUNCE_MS: 300 = 300/);
  assert.match(selection, /selectedSession[\s\S]*?queueVersion[\s\S]*?interactionMode[\s\S]*?isDrawerOpen/);
  assert.match(responsive, /useResponsive\(\)[\s\S]*?isLeftOpen[\s\S]*?onboardingAutoStartRef = useRef/);
  assert.match(rootScope, /getNodeIdForRoot[\s\S]*?scopedRootKey/);
  assert.match(taskTemplates, /useTaskTemplates\([\s\S]*?useEffect\([\s\S]*?useTaskInlineCandidates\(/);
});
