import { test } from "vitest";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

function walkMarkdown(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = path.join(dir, entry);
    if (statSync(file).isDirectory()) walkMarkdown(file, out);
    else if (file.endsWith(".md")) out.push(file);
  }
  return out;
}

function walkTestFiles(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = path.join(dir, entry);
    if (statSync(file).isDirectory()) walkTestFiles(file, out);
    else if (/\.test\.(?:mjs|ts)$/.test(file)) out.push(file);
  }
  return out;
}

test("agent rules and plan index contain no broken local Markdown links", () => {
  const sources = [
    path.join(root, "AGENTS.md"),
    ...walkMarkdown(path.join(root, "docs/agent/rules")),
    path.join(root, "docs/plans/README.md"),
  ];
  const broken = [];
  for (const source of sources) {
    const text = readFileSync(source, "utf8");
    for (const match of text.matchAll(/(?<!!)\[[^\]]*\]\(([^)]+)\)/g)) {
      const raw = match[1].trim().replace(/^<|>$/g, "").split(/\s+/, 1)[0];
      if (!raw || /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(raw)) continue;
      const target = decodeURIComponent(raw.split("#", 1)[0]);
      if (!target) continue;
      if (!existsSync(path.resolve(path.dirname(source), target))) {
        broken.push(`${path.relative(root, source)} -> ${raw}`);
      }
    }
  }
  assert.deepEqual(broken, [], `broken links:\n${broken.join("\n")}`);
});

test("plan index has at most one valid Active plan", () => {
  const index = readFileSync(path.join(root, "docs/plans/README.md"), "utf8");
  const active = [...index.matchAll(/^\|\s*\[([^\]]+)\]\(([^)]+)\)\s*\|\s*Active\s*\|/gm)];
  assert.ok(active.length <= 1, `expected at most one Active plan, found ${active.length}`);
  for (const [, , target] of active) {
    assert.ok(existsSync(path.resolve(root, "docs/plans", target)), `missing Active plan ${target}`);
  }
});

test("frontend tests use Vitest as the only runner", () => {
  const packageJSON = JSON.parse(readFileSync(path.join(root, "web/package.json"), "utf8"));
  assert.match(packageJSON.scripts.test, /vitest run/);
  assert.doesNotMatch(packageJSON.scripts.test, /node --test|test:legacy/);
  for (const file of walkTestFiles(path.join(root, "web/tests"))) {
    assert.doesNotMatch(readFileSync(file, "utf8"), /from\s+["']node:test["']/, `${file} imports node:test`);
  }
});

test("Claude and Gemini entry files point to the shared instruction and indexes", () => {
  for (const file of ["CLAUDE.md", "GEMINI.md"]) {
    const text = readFileSync(path.join(root, file), "utf8");
    for (const entry of ["@AGENTS.md", "@docs/agent/rules/README.md", "@docs/plans/README.md"]) {
      assert.ok(text.includes(entry), `${file} must include ${entry}`);
      assert.ok(existsSync(path.join(root, entry.slice(1))), `${file} points to missing ${entry}`);
    }
  }
});



test("ship instructions match the sole frontend test runner", () => {
  const ship = readFileSync(path.join(root, ".claude/skills/ship/SKILL.md"), "utf8");
  assert.match(ship, /`npm test`.*Vitest/s);
  assert.doesNotMatch(ship, /node --test|node:test|source-map-hook|ts-module-preload|Node 兼容/);
});
