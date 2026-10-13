import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { rawReadFileSync } from "./source-map.mjs";

const services = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "services");
const facades = {
  "update.ts": "./update/update",
  "appUpdate.ts": "./update/appUpdate",
  "androidUpdate.ts": "./update/androidUpdate",
  "agentConfig.ts": "./agent/agentConfig",
  "agentMemory.ts": "./agent/agentMemory",
  "agents.ts": "./agent/agents",
  "codexRateLimits.ts": "./agent/codexRateLimits",
};

test("service domain root modules remain re-export facades", () => {
  for (const [file, target] of Object.entries(facades)) {
    const source = rawReadFileSync(join(services, file), "utf8").trim();
    assert.equal(source, `export * from "${target}";`, `${file} must only preserve the public import path`);
    assert.ok(existsSync(join(services, `${target.slice(2)}.ts`)), `${file} target must exist`);
  }
});
