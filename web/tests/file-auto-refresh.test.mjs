import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

/**
 * 文件自动刷新（G-KL）的接线守卫。
 *
 * 三条通道各钉一条：WS 事件（既有）、可见时轮询、手动刷新。
 * 轮询这条最容易被合上游时整段丢掉，所以钉住「只在文件视图 + 无选中会话时发请求」
 * 与「内容没变就不 setFile」这两个否决条件 —— 前者防白烧请求，后者防预览周期性抖动。
 */
test("polling: visible-only, files view only, no churn when nothing changed", () => {
  const auto = read("../src/app/useFileAutoRefresh.ts");

  assert.match(auto, /const FILE_POLL_INTERVAL_MS = 5000;/);
  assert.match(auto, /document\.visibilityState === "hidden"\) return;/);
  assert.match(auto, /mainViewRef\.current !== "files" \|\| selectedSessionRef\.current\) return false;/);
  // 页面重新可见/重新获得焦点时立刻补一轮，不等下一个 5s。
  assert.match(auto, /document\.addEventListener\("visibilitychange", onVisible\)/);
  assert.match(auto, /window\.addEventListener\("focus", onVisible\)/);
  // 变化顺带刷新所在目录。
  assert.match(auto, /refreshTreeDir\(current\.root, dirnameOfPath\(current\.path\), false\)/);
  // 手动刷新走 force，并对外报成败。
  assert.match(auto, /refreshingFile|reloadingFile/);
  assert.match(auto, /force \? \{ force: true \} : undefined/);
});

test("refreshCurrentFileContent: force bypasses caches and reports change", () => {
  const refresh = read("../src/app/useFileRefresh.ts");

  assert.match(refresh, /options\?: \{ force\?: boolean \}/);
  assert.match(refresh, /const force = options\?\.force === true;/);
  // 手动刷新要连内存/IndexedDB 缓存和 old mtime 的 304 校验一起跳过。
  assert.match(refresh, /if \(force\) invalidateFileCache\(rootID, changedPath\);/);
  assert.match(refresh, /fresh: force \|\| undefined/);
  // 内容没变就不 setFile：否则每轮轮询都换一个新对象，预览/选区/滚动跟着抖。
  assert.match(refresh, /next\.mtime !== latestFile\.mtime/);
  assert.match(refresh, /next\.size !== latestFile\.size/);
  assert.match(refresh, /next\.content !== latestFile\.content/);
  assert.match(refresh, /if \(!changed\) return false;/);
  assert.match(refresh, /return true;/);
});

test("file viewer exposes the manual reload button and threads the callbacks", () => {
  const viewer = read("../src/components/file/FileViewer.tsx");
  const types = read("../src/components/file/useFileViewer.ts");
  const pane = read("../src/components/app/sessionPaneTypes.ts");
  const mainContent = read("../src/components/app/SessionMainContent.tsx");
  const rootView = read("../src/app/AppRootView.tsx");
  const composition = read("../src/app/useAppViewComposition.ts");

  assert.match(types, /onReloadFile\?: \(\) => void \| Promise<unknown>;/);
  assert.match(types, /reloadingFile\?: boolean;/);
  assert.match(viewer, /onClick=\{\(\) => \{ void handleReloadFile\(\); \}\}/);
  assert.match(viewer, /showToast\(t\("fileViewer\.reloadFailed"\), false\)/);
  assert.match(viewer, /<RefreshIcon spinning=\{reloadingFile\} \/>/);

  // 属性从 app 层一路穿到 FileViewer（同 refreshTreeDir 的既有穿法）。
  assert.match(composition, /const \{ onReloadFile, reloadingFile \} = useFileAutoRefresh\(\{/);
  assert.match(composition, /onReloadFile, reloadingFile,/);
  assert.match(rootView, /onReloadFile, reloadingFile,/);
  assert.match(rootView, /onReloadFile=\{onReloadFile\}/);
  assert.match(rootView, /reloadingFile=\{reloadingFile\}/);
  assert.match(pane, /onReloadFile: \(\) => Promise<boolean>;/);
  assert.match(mainContent, /onReloadFile=\{onReloadFile\}/);
  assert.match(mainContent, /reloadingFile=\{reloadingFile\}/);
});
