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

// 单入口契约（AGENTS.md 必须导入两个索引、不得存在 CLAUDE.md/GEMINI.md 壳、
// Gemini 由 .gemini/settings.json 点名）由 agent-docs.test.mjs 统一守着，此处不重复。

test("ship instructions match the sole frontend test runner", () => {
  const ship = readFileSync(path.join(root, ".claude/skills/ship/SKILL.md"), "utf8");
  assert.match(ship, /`npm test`.*Vitest/s);
  assert.doesNotMatch(ship, /node --test|node:test|source-map-hook|ts-module-preload|Node 兼容/);
});
