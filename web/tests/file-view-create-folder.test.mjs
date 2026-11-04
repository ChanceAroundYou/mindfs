// 文件视图「新建文件夹」的接线契约。
//
// 钉住四件事：
//   1) DefaultListViewMenu 里出现「新建文件夹」按钮，onClick 调 onCreateFolder；
//   2) prop 链路四层都在（DefaultListView → 菜单、SessionMainContent → DefaultListView、
//      AppRootView → SessionMainContent、sessionPaneTypes 声明）；
//   3) handler 走 appURL(..., nodeId) + kind:"dir"（跨节点不打错机器）；
//   4) 3 个 i18n key 两份 locale 齐备。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

test("create folder button is wired in the view menu", async () => {
  const menu = read("src/components/file/DefaultListViewMenu.tsx");

  assert.match(menu, /onCreateFolder/, "the menu accepts an onCreateFolder prop");
  assert.match(menu, /t\("directory\.createFolder"\)/, "the menu renders the createFolder label");
  assert.match(menu, /void onCreateFolder\?\.\(\)/, "clicking calls onCreateFolder");
});

test("create folder prop flows through the app wiring", async () => {
  const listView = read("src/components/file/DefaultListView.tsx");
  const sessionMain = read("src/components/app/SessionMainContent.tsx");
  const appRoot = read("src/app/AppRootView.tsx");
  const paneTypes = read("src/components/app/sessionPaneTypes.ts");

  assert.match(listView, /onCreateFolder=\{onCreateFolder\}/, "DefaultListView forwards to the menu");
  assert.match(sessionMain, /onCreateFolder=\{handleCreateFolder\}/, "SessionMainContent forwards the handler");
  assert.match(appRoot, /handleCreateFolder=\{handleCreateFolder\}/, "AppRootView forwards the handler");
  assert.match(paneTypes, /handleCreateFolder: \(\) => Promise<void>;/, "sessionPaneTypes declares it");
});

test("create folder handler uses the node-aware URL and kind=dir", async () => {
  const actions = read("src/app/useSessionListActions.ts");

  assert.match(actions, /directory\.folderNamePrompt/, "prompts for a folder name");
  assert.match(actions, /kind: "dir"/, "sends kind=dir");
  assert.match(actions, /appURL\("\/api\/file", new URLSearchParams\(\{ root: rootID \}\), getNodeIdForRoot\(rootID\)\)/, "uses the node-aware URL (not appPath)");
});

test("create folder i18n keys exist in both locales", async () => {
  const zh = read("src/i18n/locales/zh-CN-projects.ts");
  const en = read("src/i18n/locales/en-US-projects.ts");

  for (const key of ["directory.createFolder", "directory.folderNamePrompt", "directory.createFolderFailed"]) {
    assert.match(zh, new RegExp(`"${key.replace(".", "\\.")}":`), `${key} in zh`);
    assert.match(en, new RegExp(`"${key.replace(".", "\\.")}":`), `${key} in en`);
  }
});

console.log("file-view-create-folder.test.mjs: OK");