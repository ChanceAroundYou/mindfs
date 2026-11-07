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

// 单入口契约（AGENTS.md 必须导入两个索引、不得存在 CLAUDE.md/GEMINI.md 壳）
// 由 agent-docs.test.mjs 统一守着，此处不重复。

test("ship instructions match the sole frontend test runner", () => {
  const ship = readFileSync(path.join(root, ".claude/skills/ship/SKILL.md"), "utf8");
  assert.match(ship, /`npm test`.*Vitest/s);
  assert.doesNotMatch(ship, /node --test|node:test|source-map-hook|ts-module-preload|Node 兼容/);
});

// 部署收尾范围（G-AL）：deploy-all.sh 实际调三支 worker 部署脚本，文档必须一起说到。
// 漏写 pc-win 的表现是「有人以为 Windows 那台要手工更新」，或照着脚本自称的「已重启」
// 把「装了但没跑」当上线证据汇报。
test("deploy coverage in docs matches the three worker scripts the runner invokes", () => {
  const runner = readFileSync(path.join(root, "scripts/deploy-all.sh"), "utf8");
  for (const script of ["deploy-wsl.sh", "deploy-fn.sh", "deploy-pcwin.sh"]) {
    assert.match(runner, new RegExp(`scripts/${script}`), `deploy-all.sh 未调用 scripts/${script}`);
  }

  const docs = {
    "AGENTS.md": readFileSync(path.join(root, "AGENTS.md"), "utf8"),
    ".claude/skills/ship/SKILL.md": readFileSync(path.join(root, ".claude/skills/ship/SKILL.md"), "utf8"),
    "docs/agent/rules/common/commit-and-customization.md": readFileSync(
      path.join(root, "docs/agent/rules/common/commit-and-customization.md"),
      "utf8",
    ),
  };
  for (const [name, text] of Object.entries(docs)) {
    assert.match(text, /pc-win/, `${name} 没提 pc-win（收尾范围写漏）`);
  }

  // pc-win 的启动必须被核对：脚本要取回进程 PID，取不到要明说「未确认在跑」。
  // （2026-11-07 之前它把 schtasks /run 与 Get-Process 的输出一起丢掉、还打「已重启」。）
  const pcwin = readFileSync(path.join(root, "scripts/deploy-pcwin.sh"), "utf8");
  assert.match(pcwin, /\$\(ssh[^\n]*Get-Process mindfs/, "deploy-pcwin.sh 没有取回进程 PID");
  assert.match(pcwin, /未确认在跑/, "deploy-pcwin.sh 启动失败时没有如实说明");
  // 只禁「丢掉 stdout」：`2>/dev/null` 是 ssh 的 stderr，合法；` >/dev/null` 才是把查询结果扔了。
  assert.doesNotMatch(
    pcwin,
    /Get-Process mindfs[^\n]*[^2\d]>\s*\/dev\/null/,
    "deploy-pcwin.sh 又把进程查询结果丢掉了",
  );
  for (const name of ["AGENTS.md", ".claude/skills/ship/SKILL.md"]) {
    assert.match(docs[name], /findstr :7332/, `${name} 没写独立核对 pc-win 的判据`);
  }
});
