// HTML 预览「真正当网页跑」的机器可核对契约（2026-11-09 升级）。
//
// 钉住五件事：
//   1) 静态体检能认出构建前的开发入口（.ts/.tsx/.jsx/.vue/.svelte、/@vite/ 注入）；
//   2) 普通网页（.js、CDN 模块、内联脚本）不被误判；
//   3) iframe sandbox 与后端 CSP 响应头**同时**带 allow-same-origin —— 这是同一条
//      决策的两半，改一边页面就会重新被隔离（localStorage / IndexedDB 再次抛错）；
//   4) FileViewer 把文件内容交给预览做体检；
//   5) 后端有「根绝对路径兜底」的改写函数（/assets/x → 项目根）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { findUnrunnableScripts, isDevEntryHtml } from "../src/services/htmlPreview.ts";

const root = path.resolve(import.meta.dirname, "..", "..");
const read = (file) => fs.readFileSync(path.join(root, file), "utf8");

test("detects pre-build dev entries the browser cannot run", () => {
  const viteEntry = `<!doctype html><html><body><div id="root"></div>
    <script type="module" src="./src/main.tsx"></script></body></html>`;
  assert.deepEqual(findUnrunnableScripts(viteEntry), ["./src/main.tsx"]);
  assert.equal(isDevEntryHtml(viteEntry), true);

  assert.deepEqual(findUnrunnableScripts(`<script src="/app/main.jsx"></script>`), ["/app/main.jsx"]);
  assert.deepEqual(findUnrunnableScripts(`<script src="widget.vue?v=2"></script>`), ["widget.vue?v=2"]);
  assert.deepEqual(findUnrunnableScripts(`<script src="/@vite/client"></script>`), ["/@vite/client"]);

  // 去重且保序
  assert.deepEqual(
    findUnrunnableScripts(`<script src="a.ts"></script><script src="a.ts"></script><script src="b.jsx"></script>`),
    ["a.ts", "b.jsx"],
  );
});

test("does not flag ordinary web pages", () => {
  const plain = `<!doctype html><html><head><link rel="stylesheet" href="./style.css"></head>
    <body><img src="./logo.png"><script src="./app.js"></script>
    <script type="module" src="https://cdn.example.com/lib.mjs"></script>
    <script>console.log("inline")</script></body></html>`;
  assert.deepEqual(findUnrunnableScripts(plain), []);
  assert.equal(isDevEntryHtml(plain), false);
  assert.deepEqual(findUnrunnableScripts(""), []);
  assert.deepEqual(findUnrunnableScripts(null), []);
});

test("preview sandbox and CSP both allow same-origin", () => {
  const preview = read("web/src/components/file/HtmlPreview.tsx");
  assert.match(preview, /sandbox="allow-scripts allow-same-origin"/);
  assert.match(preview, /onLoad=\{handleFrameLoad\}/);

  const handler = read("server/internal/api/http_raw_files.go");
  assert.match(handler, /sandbox allow-scripts allow-same-origin/);

  const hook = read("web/src/hooks/useHtmlPreviewFrame.ts");
  assert.match(hook, /addEventListener\(\s*"error"/);
  assert.match(hook, /unhandledrejection/);
  // 解析期抛的错在 iframe load 之前就发生，只能靠后端探针 postMessage 回传。
  assert.match(hook, /addEventListener\("message"/);
  assert.match(hook, /event\.source !== frame\.contentWindow/);
});

test("file viewer feeds content into the preview check", () => {
  const viewer = read("web/src/components/file/FileViewer.tsx");
  assert.match(viewer, /<HtmlPreview[^>]*content=\{file\.content\}/);
});

test("backend rewrites root-absolute refs back to the project root", () => {
  const rewrite = read("server/internal/api/raw_asset_html.go");
  assert.match(rewrite, /func rewriteRootAbsoluteHTML/);
  assert.match(rewrite, /regexp\.MustCompile\(`\(\?i\)\(\\b\(\?:src\|href\|poster\)/);
  assert.match(rewrite, /func relativeProjectRootPrefix/);
  assert.match(rewrite, /func prepareHTMLForPreview/);
  assert.match(rewrite, /const previewErrorProbeMarker/);
});
