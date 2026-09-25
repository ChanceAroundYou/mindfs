import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 这些变量被各处引用，但**从未**在任何 CSS 里定义过。浏览器解析不出 var() 时会按
// invalid at computed-value time 整条丢弃声明 —— 于是依赖它们的属性一直静默退化成
// 「无颜色 / 透明」（曾导致任务面板禁用按钮没有灰底）。index.css 里的 :root 别名块
// 是唯一补口，回归即此处被删。
const root = path.resolve(import.meta.dirname, "..");
const src = path.join(root, "src");
const css = fs.readFileSync(path.join(src, "index.css"), "utf8");

const ALIASES = ["--text-color", "--muted-text", "--input-bg", "--background-color", "--bg-primary", "--button-bg"];

for (const name of ALIASES) {
  // 值可以是 var() 映射，也可以是具体色值；重要的是这个变量真的被定义了。
  assert.match(
    css,
    new RegExp(`${name}\\s*:\\s*[^;\\n]+`),
    `${name} must be defined in index.css :root (was referenced but never defined)`,
  );
}

// 别名必须在 :root 里、而不是某个主题块里 —— var() 在根元素上求值，
// 各 data-theme 覆盖 --text-primary 等之后别名照样跟随。
const rootBlock = css.slice(css.indexOf(":root {"), css.indexOf("\n}", css.indexOf(":root {")));
assert.ok(rootBlock.length > 0, "index.css must keep a :root block");
for (const name of ALIASES) {
  assert.ok(rootBlock.includes(`${name}:`), `${name} alias must live in the :root block (theme blocks override individually)`);
}

// 全仓库扫描：任何 var(--x) 若既没定义、也没 fallback，就是下一个静默失效点。
const files = [];
(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name === "node_modules" || e.name === "dist") continue;
      walk(p);
    } else if (/\.(tsx?|css|mjs)$/.test(e.name)) {
      files.push([path.relative(root, p), fs.readFileSync(p, "utf8")]);
    }
  }
})(src);

const defined = new Set();
const used = new Map();
const runtimeSet = new Set();
for (const [, text] of files) {
  for (const m of text.matchAll(/(?<![\w-])(--[A-Za-z0-9_-]+)\s*:/g)) defined.add(m[1]);
  for (const m of text.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)\s*([,)])/g)) {
    if (!used.has(m[1])) used.set(m[1], { total: 0, bare: 0 });
    const e = used.get(m[1]);
    e.total += 1;
    if (m[2] === ")") e.bare += 1; // 无 fallback
  }
  for (const m of text.matchAll(/["'](--[A-Za-z0-9_-]+)["']\s*(?::|as any)/g)) runtimeSet.add(m[1]);
}
// TokenEditor / AppShell / MarkdownViewer 通过 style 对象注入的变量。
const orphans = [];
for (const [name, e] of used) {
  if (defined.has(name) || runtimeSet.has(name)) continue;
  if (e.bare > 0) orphans.push(`${name} (${e.total} uses)`);
}
assert.deepEqual(
  orphans,
  [],
  `var() used without definition and without fallback silently drops the whole declaration: ${orphans.join(", ")}`,
);

console.log("css-variable-aliases.test.mjs: OK");
