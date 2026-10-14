import { test } from "vitest";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "./source-map-fs.mjs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const read = (path) => readFileSync(join(ROOT, path), "utf8");

function walkMarkdown(dir) {
  const files = [];
  for (const entry of readdirSync(join(ROOT, dir))) {
    const path = join(dir, entry);
    if (statSync(join(ROOT, path)).isDirectory()) files.push(...walkMarkdown(path));
    else if (path.endsWith(".md")) files.push(path);
  }
  return files;
}

function brokenRelativeLinks(markdownFiles) {
  const failures = [];
  for (const file of markdownFiles) {
    const text = read(file);
    for (const [, target] of text.matchAll(/\]\(([^)]+)\)/g)) {
      if (/^(?:https?:|mailto:|#)/i.test(target)) continue;
      const path = target.split("#", 1)[0];
      if (!path) continue;
      if (!statSync(join(ROOT, dirname(file), path), { throwIfNoEntry: false })) {
        failures.push(`${file}: ${target}`);
      }
    }
  }
  return failures;
}

test("agent entrypoints import the shared instructions and indexes", () => {
  for (const file of ["CLAUDE.md", "GEMINI.md"]) {
    const text = read(file);
    for (const imported of ["@AGENTS.md", "@docs/agent/rules/README.md", "@docs/plans/README.md"]) {
      assert.ok(text.includes(imported), `${file} must import ${imported}`);
      const path = imported.slice(1);
      assert.ok(statSync(join(ROOT, path), { throwIfNoEntry: false }), `${file} imports missing ${path}`);
    }
  }
  const instructions = read("AGENTS.md");
  assert.ok(instructions.includes("docs/agent/rules/README.md"));
  assert.ok(instructions.includes("docs/plans/README.md"));
  assert.ok(read(".claude/rules/README.md").includes("docs/agent/rules/"));
  const claudeSkill = read(".claude/skills/upstream-merge/SKILL.md");
  assert.ok(claudeSkill.includes("sudo systemctl restart mindfs"));
  assert.ok(!claudeSkill.includes("systemctl --user restart mindfs.service"));
  assert.ok(!claudeSkill.includes("export PATH="));
});

test("agent rule and plan index links resolve", () => {
  const files = [
    ...walkMarkdown("docs/agent/rules"),
    "docs/plans/README.md",
  ];
  assert.deepEqual(brokenRelativeLinks(files), []);
});

test("every plan is classified in the sole plans index", () => {
  const index = read("docs/plans/README.md");
  const plans = readdirSync(join(ROOT, "docs/plans")).filter(
    (name) => name.endsWith(".md") && name !== "README.md",
  );
  const unclassified = plans.filter((name) => {
    const row = index.split("\n").find((line) => line.includes(`(${name})`));
    return !row || !row.startsWith("|") || !row.split("|")[2]?.trim();
  });
  assert.deepEqual(unclassified, [], "Every plan must have a status row in docs/plans/README.md");
});
