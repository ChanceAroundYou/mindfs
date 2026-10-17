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

test("AGENTS.md is the sole agent entrypoint and imports the shared indexes", () => {
  // 收束为单入口（2026-10-17）：CLAUDE.md / GEMINI.md 这两个「只导入、无正文」的壳已删。
  // 留壳不是无害的重复 —— Claude Code 的 agents-md 插件在**当前目录或其任一祖先**存在
  // CLAUDE.md 时就不再发现 AGENTS.md，壳会把共享正文整个挡在外面。
  for (const shell of ["CLAUDE.md", "GEMINI.md", "CLAUDE.local.md", ".claude/CLAUDE.md"]) {
    assert.ok(!statSync(join(ROOT, shell), { throwIfNoEntry: false }), `${shell} 复活了：它会顶掉 AGENTS.md`);
  }
  const instructions = read("AGENTS.md");
  for (const imported of ["@docs/agent/rules/README.md", "@docs/plans/README.md"]) {
    assert.ok(instructions.includes(imported), `AGENTS.md must import ${imported}`);
    const path = imported.slice(1);
    assert.ok(statSync(join(ROOT, path), { throwIfNoEntry: false }), `AGENTS.md imports missing ${path}`);
  }
  // 这里**故意不钉**「某个工具的点名文件」：Gemini CLI 的 .gemini/settings.json 曾经加过，
  // 后来删了（本机没装 gemini，没有消费者；预埋一份配置只会腐烂）。等真有工具不认
  // AGENTS.md 自动发现时再加，并连同它的点名文件一起钉。
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

