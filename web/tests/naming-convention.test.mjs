// Naming rules are universal: no path-based grandfather list.
import { test } from "vitest";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "./source-map-fs.mjs";
import { join, basename, extname } from "node:path";
import { fileURLToPath } from "node:url";
import { transformSync } from "esbuild";
import { rawReadFileSync } from "./source-map.mjs";

const SRC = join(fileURLToPath(import.meta.url), "..", "..", "src");

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = join(dir, entry);
    if (statSync(file).isDirectory()) {
      if (entry !== "node_modules") walk(file, out);
    } else if (file.endsWith(".ts") || file.endsWith(".tsx")) out.push(file);
  }
  return out;
}

function tsHasJsx(source) {
  try {
    transformSync(source, { loader: "ts" });
    return false;
  } catch (error) {
    return /expected ">"|unterminated|JSX|jsx/i.test(String(error?.message || error));
  }
}

test(".ts files contain no JSX", () => {
  const violations = walk(SRC).filter((file) => file.endsWith(".ts") && !file.endsWith(".d.ts"))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: contains JSX`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("Utils files contain no JSX", () => {
  const violations = walk(SRC).filter((file) => /Utils\.ts$/.test(file))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: *Utils.ts must contain pure logic`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("JSX helpers use Helpers.tsx", () => {
  const violations = walk(SRC).filter((file) => /Utils\.tsx$/.test(file))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: JSX helpers must use *Helpers.tsx`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("component domain types use a domain-specific name", () => {
  const violations = walk(SRC).filter((file) => /components\/[^/]+\/types\.ts$/.test(file.slice(SRC.length + 1)))
    .map((file) => `${file}: use a domain-specific *Types.ts name`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("style modules use Styles.ts and contain no JSX", () => {
  const violations = walk(SRC).filter((file) => /Styles\.tsx$/.test(basename(file)))
    .map((file) => `${file}: style modules must use *Styles.ts`);
  assert.deepEqual(violations, [], violations.join("\n"));
});
