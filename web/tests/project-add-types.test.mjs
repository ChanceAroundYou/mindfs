import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const sharedTypes = new URL("../src/shared/projectAddTypes.ts", import.meta.url);
const read = (url) => readFileSync(url, "utf8");

test("project-add state types are shared without app logic importing the view", () => {
  const types = read(sharedTypes);
  assert.match(types, /export type ProjectAddMode\s*=/);
  assert.match(types, /export type GitHubImportState\s*=/);
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
  assert.match(view, /export type \{ GitHubImportState, ProjectAddMode \} from "\.\.\/\.\.\/shared\/projectAddTypes"/);
});
