import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

/**
 * 「同名文件被改写后还拿到旧版」（G-KL）的缓存分键守卫。
 *
 * 五层缓存各自都要有版本分键，缺一层就是同一个 bug 的另一个出口：
 *   blob Map（前端 JS） → URL 查询串（浏览器 HTTP 缓存） → hook 依赖（React 不重跑）
 *   → memo 比较器（组件不重渲染） → 服务端 Cache-Control（中间层/浏览器长期复用）。
 */
test("blob cache and raw asset URLs are keyed by the file version", () => {
  const blob = read("../src/services/file/blob.ts");
  const rawAsset = read("../src/services/file/rawAsset.ts");
  const hook = read("../src/hooks/useProtectedBlobUrl.ts");
  const rawHook = read("../src/hooks/useRawAssetUrl.ts");

  // blob Map 的 key 带上版本：版本变了就是另一个条目，不吃「同路径旧图」。
  assert.match(blob, /version\?: string/);
  assert.match(blob, /const cacheKey = `\$\{params\.rootId\}:\$\{params\.path\}:\$\{params\.nodeId \|\| ""\}:\$\{params\.version \|\| ""\}`/);
  assert.match(blob, /target\.searchParams\.set\("v", version\)/);
  // 失效路径按前缀批量清 —— 版本段在末尾，前缀仍命中。
  assert.match(read("../src/services/file/fetch.ts"), /key\.startsWith\(`\$\{rootId\}:\$\{path\}:`\)/);

  // 路径式原始资源（网页/视频）同样带版本。
  assert.match(rawAsset, /version\?: string/);
  assert.match(rawAsset, /return params\.version \? `\$\{url\}\?v=\$\{encodeURIComponent\(params\.version\)\}` : url;/);

  // 两个 hook 的依赖里必须有版本，否则 React 认为「请求没变」不重跑。
  assert.match(hook, /request\?\.version\]/);
  assert.match(rawHook, /\[rootId, path, nodeId, version\]/);
});

test("preview components take the version through", () => {
  const fileViewer = read("../src/components/file/FileViewer.tsx");
  const documentViewer = read("../src/components/file/DocumentViewer.tsx");
  const documentBlob = read("../src/components/file/useDocumentPreviewBlob.ts");
  const video = read("../src/components/file/VideoPreview.tsx");
  const html = read("../src/components/file/HtmlPreview.tsx");
  const image = read("../src/components/file/ImageViewer.tsx");

  // 单一版本源：文件 mtime（没有就退回 revision）。
  assert.match(fileViewer, /const fileVersion = file\?\.mtime \|\| file\?\.revision \|\| ""/);
  for (const component of [documentViewer, video, html, image]) {
    assert.match(component, /version\?: string/, `${component} missing version prop`);
  }
  for (const component of [documentViewer, video, html, image]) {
    assert.match(component, /prev(?:ious)?\.version === next\.version/, `${component} memo comparator misses version`);
  }
  assert.match(documentBlob, /fetchProofProtectedBlob\(\{ rootId: root, path, version \}\)/);
  assert.match(documentBlob, /\[path, root, version\]/);
});

test("raw endpoints tell browsers to revalidate instead of heuristically caching", () => {
  const rawRoute = read("../../server/internal/api/http_raw_files.go");
  const queryRoute = read("../../server/internal/api/http_files.go");

  // 路径式：Last-Modified 在，no-cache 让浏览器每次回源确认（304 很便宜）。
  assert.match(rawRoute, /w\.Header\(\)\.Set\("Cache-Control", "no-cache"\)/);
  // 查询式（blob 拉取）：前端自己按版本缓存，HTTP 层不许再留一份。
  assert.match(queryRoute, /w\.Header\(\)\.Set\("Cache-Control", "no-store"\)/);
});
