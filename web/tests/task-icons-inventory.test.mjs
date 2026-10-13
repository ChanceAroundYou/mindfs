import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = join(fileURLToPath(new URL("..", import.meta.url)), "src/app/taskIcons.tsx");

test("taskIcons export inventory stays aligned with its public exports", () => {
  const source = readFileSync(root, "utf8");
  const inventory = source.match(/Export inventory[\s\S]*?INVENTORY-END/)?.[0];
  assert.ok(inventory, "taskIcons.tsx must declare a bounded export inventory");

  const listed = [...inventory.matchAll(/\b[A-Za-z_$][\w$]*\b/g)]
    .map(([name]) => name)
    .filter((name) => !["Export", "inventory", "kept", "in", "sync", "by", "task", "icons", "test", "mjs", "Icons", "Task", "styles", "Color", "utility", "INVENTORY", "END"].includes(name));
  const declared = [...source.matchAll(/^export\s+(?:function|type)\s+([A-Za-z_$][\w$]*)/gm)]
    .map(([, name]) => name);

  assert.deepEqual([...listed].sort(), [...declared].sort());
});
