import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const sharedTypes = new URL("../src/shared/projectAddTypes.ts", import.meta.url);
const read = (url) => readFileSync(url, "utf8");

test("project-add state types are shared without app logic importing the view", () => {
  const types = read(sharedTypes);
  assert.match(types, /export type ProjectAddMode\s*=/);
  assert.match(types, /export type GitHubImportState\s*=/);
  assert.match(types, /export type LocalDirBrowserState\s*=/);
  assert.match(types, /export type LocalDirItem\s*=/);
  assert.doesNotMatch(types, /from\s+["'][^"']*(?:components|services|react)\//);

  for (const file of [
    "../src/app/useRealtimeEvents.ts",
    "../src/app/useRealtimeEventsContext.ts",
    "../src/app/realtimeWsHandlers.ts",
  ]) {
    const source = read(new URL(file, import.meta.url));
    assert.match(source, /shared\/projectAddTypes/);
    assert.doesNotMatch(source, /from\s+["'][^"']*components\/file\/ProjectAddPopover/);
  }

  const view = read(new URL("../src/components/file/ProjectAddPopover.tsx", import.meta.url));
  assert.match(view, /export type \{ GitHubImportState, LocalDirBrowserState, LocalDirItem, ProjectAddMode \} from "\.\.\/\.\.\/shared\/projectAddTypes"/);
  const lifecycle = read(new URL("../src/app/useProjectLifecycle.tsx", import.meta.url));
  assert.match(lifecycle, /import type \{ GitHubImportState, ProjectAddMode \} from "\.\.\/shared\/projectAddTypes"/);
  assert.doesNotMatch(lifecycle, /type (?:GitHubImportState|LocalDirBrowserState|ProjectAddMode) from "\.\.\/components\/file\/ProjectAddPopover"/);
});

test("project-add mode panel keeps all three mode callbacks in its view boundary", () => {
  const panel = read(new URL("../src/components/file/ProjectAddModePanel.tsx", import.meta.url));
  for (const key of ["projectAdd.blankProject", "projectAdd.localDirectory", "projectAdd.githubImport"]) {
    assert.ok(panel.includes(`t("${key}")`), `mode panel keeps ${key}`);
  }
  for (const callback of ["onSelectBlankLocation", "onSelectLocal", "onSelectGitHubLocation"]) {
    assert.match(panel, new RegExp(`onClick=\\{${callback}\\}`), `mode panel wires ${callback}`);
  }
  assert.match(panel, /export function ProjectAddModePanel/);
});

test("project-add actions own GitHub import routing while the lifecycle view composes the existing popover", () => {
  const actions = read(new URL("../src/app/useProjectAddActions.ts", import.meta.url));
  const view = read(new URL("../src/components/file/ProjectAddPopover.tsx", import.meta.url));
  const lifecycle = read(new URL("../src/app/useProjectLifecycle.tsx", import.meta.url));
  assert.match(actions, /if \(!url \|\| !parentPath \|\| state\.running \|\| state\.submitting\) return/);
  assert.match(actions, /appPath\("\/api\/imports\/github", targetImportNodeId\)/);
  assert.match(actions, /status: "pending"[\s\S]*?running: true/);
  assert.match(view, /export function ProjectAddLifecycleOverlay/);
  assert.match(view, /mode === "local" \? t\("projectAdd\.add"\) : t\("projectAdd\.placeHere"\)/);
  assert.match(lifecycle, /<ProjectAddLifecycleOverlay[\s\S]*?onGitHubImport=\{\(\) => void handleGitHubImportStart\(githubImportState\)\}/);
});
