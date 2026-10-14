import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");

test("domain layer keeps its type facade and delegates project management below the hard line", () => {
  const layer = read("src/app/useAppDomainLayer.ts");
  const types = read("src/app/useAppDomainLayerTypes.ts");
  const project = read("src/app/useAppProjectManagement.ts");
  const derived = read("src/app/useAppDomainDerived.ts");
  const app = read("src/App.tsx");

  assert.ok(layer.split("\n").length - 1 < 500, "domain layer must stay below the 500-line hard line");
  assert.match(types, /export type UseAppDomainLayerParams =/);
  assert.match(layer, /export type \{ UseAppDomainLayerParams \} from "\.\/useAppDomainLayerTypes"/);
  assert.match(derived, /from "\.\/useAppDomainLayer"/);
  for (const hook of ["useManagedRoots", "useActionHandlers", "useAppBoundSessionDir", "useRelatedFileDiff", "useAppFileNavigation", "useAppProjectRemoval", "useProjectLifecycle"]) {
    assert.match(project, new RegExp(`${hook}\\(`), `${hook} remains in the project-management group`);
  }
  assert.match(layer, /useAppProjectManagement\(params\)/);
  assert.match(layer, /useAppProjectManagement\(params\)[\s\S]*?usePluginHandlers\(/);
  assert.match(app, /useAppDomainLayer\(\{ \.\.\.l1, \.\.\.l2 \}\)/);
});
