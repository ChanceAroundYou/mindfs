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

test("AGENTS.md is the sole agent entrypoint and keeps only the small shared rules import", () => {
  // 收束为单入口（2026-10-17）：CLAUDE.md / GEMINI.md 这两个「只导入、无正文」的壳已删。
  // 留壳不是无害的重复 —— Claude Code 的 agents-md 插件在**当前目录或其任一祖先**存在
  // CLAUDE.md 时就不再发现 AGENTS.md，壳会把共享正文整个挡在外面。
  for (const shell of ["CLAUDE.md", "GEMINI.md", "CLAUDE.local.md", ".claude/CLAUDE.md"]) {
    assert.ok(!statSync(join(ROOT, shell), { throwIfNoEntry: false }), `${shell} 复活了：它会顶掉 AGENTS.md`);
  }
  const instructions = read("AGENTS.md");
  assert.ok(instructions.includes("@docs/agent/rules/README.md"), "规则索引仍须自动注入");
  // Claude 会展开 @import；把随每次发版增长的 plans 索引内联进来，会让 session_start
  // 注入 147k chars，越过 CLI 本地约 177k 的拒发线。它必须改成按需读取。
  assert.ok(!instructions.includes("@docs/plans/README.md"), "plans 索引不得自动注入");
  assert.ok(instructions.includes("docs/plans/README.md"), "AGENTS.md 仍须点名 plans 索引供按需读取");
  assert.ok(instructions.includes("DEPLOY_HISTORY.md"), "AGENTS.md 仍须点名发布历史供按需读取");
  assert.ok(statSync(join(ROOT, "docs/plans/DEPLOY_HISTORY.md"), { throwIfNoEntry: false }), "发布历史文件缺失");
  // 这里**故意不钉**「某个工具的点名文件」：Gemini CLI 的 .gemini/settings.json 曾经加过，
  // 后来删了（本机没装 gemini，没有消费者；预埋一份配置只会腐烂）。等真有工具不认
  // AGENTS.md 自动发现时再加，并连同它的点名文件一起钉。
  assert.ok(read(".claude/rules/README.md").includes("docs/agent/rules/"));
  const claudeSkill = read(".claude/skills/upstream-merge/SKILL.md");
  assert.ok(claudeSkill.includes("sudo systemctl restart mindfs"));
  assert.ok(!claudeSkill.includes("systemctl --user restart mindfs.service"));
  assert.ok(!claudeSkill.includes("export PATH="));
});

test("instruction injection stays under every harness budget", () => {
  const instructions = read("AGENTS.md");
  assert.ok(Buffer.byteLength(instructions, "utf8") <= 8 * 1024, "AGENTS.md 必须 <= 8 KiB（Codex 32 KiB 上限留余量）");
  const imports = [...instructions.matchAll(/^@(\S+)$/gm)].map(([, path]) => path);
  assert.deepEqual(imports, ["docs/agent/rules/README.md"], "自动展开只保留规则索引");
  let expanded = instructions.length;
  for (const path of imports) {
    const text = read(path);
    assert.ok(text.length <= 16_000, `${path} 不得超过 16k chars`);
    expanded += text.length;
  }
  assert.ok(expanded <= 32_000, `展开后总量 ${expanded} 超过 32k chars`);

  const plans = read("docs/plans/README.md");
  assert.ok(plans.length <= 20_000, `plans 索引 ${plans.length} chars 过长，发布史应归档`);
  assert.ok(!plans.includes("部署状态（权威"), "plans 索引不得保留部署状态长段落");
  assert.ok(plans.includes("DEPLOY_HISTORY.md"), "plans 索引须给出发布历史指针");
});

test("agent rule and plan index links resolve", () => {
  const files = [
    ...walkMarkdown("docs/agent/rules"),
    "docs/plans/README.md",
  ];
  assert.deepEqual(brokenRelativeLinks(files), []);
});

