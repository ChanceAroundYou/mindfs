import { test } from "vitest";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { rawReadFileSync } from "./source-map.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

function walkSource(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = path.join(dir, entry);
    if (statSync(file).isDirectory()) walkSource(file, out);
    else if (/\.tsx?$/.test(file)) out.push(file);
  }
  return out;
}

test("components do not import services directly", () => {
  const actual = [];
  for (const file of walkSource(path.join(root, "web/src/components")).filter((name) => name.endsWith(".tsx"))) {
    const text = rawReadFileSync(file, "utf8");
    for (const match of text.matchAll(/\bfrom\s*["']([^"']*services\/[^"']+)["']/g)) {
      actual.push(`${path.relative(root, file)} -> ${match[1]}`);
    }
  }
  assert.deepEqual(actual, [], `view -> service imports are forbidden:\n${actual.join("\n")}`);
});

test("all frontend source files stay under 500 lines", () => {
  const over = walkSource(path.join(root, "web/src"))
    .map((file) => [file, rawReadFileSync(file, "utf8").split("\n").length - 1])
    .filter(([, lines]) => lines > 500)
    .map(([file, lines]) => `${path.relative(root, file)}: ${lines}`);
  assert.deepEqual(over, [], `frontend hard-line violations:\n${over.join("\n")}`);
});
