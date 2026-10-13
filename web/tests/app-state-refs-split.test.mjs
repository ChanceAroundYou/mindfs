import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");

test("state layer delegates its ordered ref and session-cache ownership", () => {
  const layer = read("src/app/useAppStateLayer.ts");
  const refs = read("src/app/useAppStateRefs.ts");

  assert.match(layer, /const \{[\s\S]*?handleSelectSessionRef,[\s\S]*?\} = useAppStateRefs\(\)/);
  assert.match(refs, /export function useAppStateRefs\(\)/);
  assert.match(refs, /const pluginManagerRef[\s\S]*?const pendingDraftRef[\s\S]*?useSessionCache\(\)[\s\S]*?const invalidTreeCacheKeysRef/);
  assert.match(refs, /fileScrollPositionsRef = useRef<Record<string, number>>\(loadPersistedFileScrollPositions\(\)\)/);
  assert.ok(layer.split("\n").length - 1 < 816, "the parent should shrink as responsibilities move out");
});
