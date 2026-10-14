import { test } from "node:test";
import assert from "node:assert/strict";
import { closeSync, openSync, readSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

function readSource(file) {
  const fd = openSync(file, "r");
  try {
    const size = statSync(file).size;
    const buffer = Buffer.alloc(size);
    readSync(fd, buffer, 0, size, 0);
    return buffer.toString("utf8");
  } finally {
    closeSync(fd);
  }
}

function walkSource(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = path.join(dir, entry);
    if (statSync(file).isDirectory()) walkSource(file, out);
    else if (/\.tsx?$/.test(file)) out.push(file);
  }
  return out;
}

test("component service imports only shrink from the reviewed legacy allowlist", () => {
  const allowlist = new Set(JSON.parse(readFileSync(path.join(root, "web/tests/fixtures/component-service-import-debt.json"), "utf8")));
  const actual = new Set();
  for (const file of walkSource(path.join(root, "web/src/components")).filter((name) => name.endsWith(".tsx"))) {
    const text = readSource(file);
    for (const match of text.matchAll(/\bfrom\s*["']([^"']*services\/[^"']+)["']/g)) {
      actual.add(`${path.relative(root, file)} -> ${match[1]}`);
    }
  }
  const unreviewed = [...actual].filter((edge) => !allowlist.has(edge)).sort();
  assert.deepEqual(unreviewed, [], `new view -> service imports are forbidden:\n${unreviewed.join("\n")}`);
});

test("frontend source files stay under 500 lines outside documented exemptions", () => {
  const sourceFiles = walkSource(path.join(root, "web/src"));
  const exemptions = new Set([
    path.join(root, "web/src/main.tsx"),
    path.join(root, "web/src/i18n/locales/en-US.ts"),
    path.join(root, "web/src/i18n/locales/zh-CN.ts"),
    path.join(root, "web/src/components/task/taskIcons.tsx"),
  ]);
  const over = sourceFiles
    .filter((file) => !exemptions.has(file))
    .map((file) => [file, readSource(file).split("\n").length - 1])
    .filter(([, lines]) => lines > 500)
    .map(([file, lines]) => `${path.relative(root, file)}: ${lines}`);
  assert.deepEqual(over, [], `frontend hard-line violations:\n${over.join("\n")}`);
});
