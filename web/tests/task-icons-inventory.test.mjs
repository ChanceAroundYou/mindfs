import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const srcRoot = join(fileURLToPath(new URL("..", import.meta.url)), "src");
const root = join(srcRoot, "components/task/taskIcons.tsx");
const stylesRoot = join(srcRoot, "components/task/taskStyles.ts");

test("taskIcons export inventory stays aligned with its public exports", () => {
  const source = readFileSync(root, "utf8");
  const styles = readFileSync(stylesRoot, "utf8");
  const inventory = source.match(/Export inventory[\s\S]*?INVENTORY-END/)?.[0];
  assert.ok(inventory, "taskIcons.tsx must declare a bounded export inventory");

  const listed = [...inventory.matchAll(/\b[A-Za-z_$][\w$]*\b/g)]
    .map(([name]) => name)
    .filter((name) => !["Export", "inventory", "kept", "in", "sync", "by", "task", "icons", "test", "mjs", "Icons", "Task", "styles", "Color", "utility", "INVENTORY", "END"].includes(name));
  const declared = [...`${source}\n${styles}`.matchAll(/^export\s+(?:function|type)\s+([A-Za-z_$][\w$]*)/gm)]
    .map(([, name]) => name);

  assert.deepEqual([...new Set(listed)].sort(), [...new Set(declared)].sort());

  const appFacade = readFileSync(join(srcRoot, "app/taskIcons.tsx"), "utf8");
  assert.match(appFacade, /export \* from "\.\.\/components\/task\/taskIcons"/);
  assert.doesNotMatch(source, /from ["']\.\.\/\.\.\/services\//);
  assert.match(source, /from "\.\.\/\.\.\/shared\/nodePalette"/);
});
