import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  PLAN_COMMAND,
  isPlanCommand,
  parsePlanCommand,
  stripPlanCommandPrefix,
  withPlanPrefix,
} from "../src/shared/planCommand.ts";

test("plan command helpers preserve input normalization and prefix behavior", () => {
  assert.equal(PLAN_COMMAND, "/plan");
  assert.equal(isPlanCommand("  /PLAN  "), true);
  assert.equal(isPlanCommand("/plan please"), true);
  assert.equal(isPlanCommand("/planner"), false);
  assert.equal(parsePlanCommand("/plan now"), true);
  assert.equal(parsePlanCommand("hello"), null);
  assert.equal(stripPlanCommandPrefix(" /plan   describe the change "), "describe the change");
  assert.equal(stripPlanCommandPrefix(" hello "), "hello");
  assert.equal(withPlanPrefix("describe the change"), "/plan describe the change");
});

test("app logic and shared plan helpers do not depend on view modules", () => {
  const root = new URL("../src/", import.meta.url);
  for (const file of ["app/useSendMessage.ts", "app/prepareSendMessageTarget.ts", "app/useSendMessageSlashCommand.ts", "app/useSendMessageTypes.ts", "app/useSessionLifecycle.ts", "app/useSessionSyncAction.ts", "app/useSessionListLoading.ts", "app/useManagedRootRename.ts", "app/useManagedRootsTypes.ts"]) {
    const source = readFileSync(new URL(file, root), "utf8");
    assert.doesNotMatch(source, /from\s+["'][^"']*(?:components|layout)\//, file);
  }
  const shared = readFileSync(fileURLToPath(new URL("../src/shared/planCommand.ts", import.meta.url)), "utf8");
  assert.doesNotMatch(shared, /from\s+["'][^"']*(?:components|services|react)\//);
});
