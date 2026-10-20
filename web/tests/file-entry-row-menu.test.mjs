// 文件浏览器「逐条三点操作菜单」的接线契约。
//
// 钉住四件事：
//   1) 菜单复用系统内既有控件（MoreMenuButton / FileOperationItems / MoveFilePopover），
//      不另造一套；
//   2) 弹层 portal 到 body + fixed 定位（列表容器 overflow:auto，行上有 translateZ(0)，
//      内联定位必被裁掉）；
//   3) 点三点不触发行点击（否则会顺手打开文件 / 进入目录）；
//   4) 操作走既有 /api/file/operation 通路（路径语义 = 相对项目根）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

test("file entry menu reuses the app menu controls", async () => {
  const menu = read("src/components/file/FileEntryMenu.tsx");

  assert.match(menu, /MoreMenuButton/, "the three-dot button is the app's own MoreMenuButton");
  assert.match(menu, /FileOperationItems/, "delete/rename/move come from the shared FileOperationItems");
  assert.match(menu, /MoveFilePopover/, "move reuses the shared directory-browser panel");
  assert.match(menu, /createPortal\(/, "the menu is portaled to body");
  assert.match(menu, /document\.body/, "the portal target is document.body");
  assert.match(menu, /position: "fixed"/, "the portaled menu is viewport-positioned");
  assert.match(menu, /stopPropagation/, "clicking the three dots must not trigger the row click");
  assert.match(menu, /t\("fileOperation\.menuFor", \{ name \}\)/, "the button label names the file");

  // 视图层不许直接碰 services/：数据与副作用走既有 hook。
  assert.doesNotMatch(menu, /services\/(?:dialog|net\/api|net\/base|net\/error)/);
});

test("file entry row renders the menu on the right of every entry", async () => {
  const row = read("src/components/file/FileEntryRow.tsx");
  const listView = read("src/components/file/DefaultListView.tsx");

  assert.match(row, /<FileEntryMenu/, "each row renders the three-dot menu");
  assert.match(row, /path=\{entry\.path\}/, "the operation targets the entry path (relative to the project root)");
  assert.match(row, /root=\{root\}/, "the operation carries the project id");
  assert.match(row, /onFileOperationComplete/, "the row refresh callback is passed through");
  // 菜单在名称与元信息之后 —— 即最右侧。
  assert.ok(
    row.indexOf("<FileEntryMenu") > row.indexOf("showCompactMeta"),
    "the menu must come after the name/meta columns (right-most)",
  );

  assert.match(listView, /<FileEntryRow/, "the list renders rows through FileEntryRow");
  assert.doesNotMatch(listView, /<FileEntryIcon/, "the row markup moved into FileEntryRow");
});

test("file entry menu uses the shared file operation endpoint", async () => {
  const hooks = read("src/hooks/useFileOperationActions.ts");
  const move = read("src/hooks/useFileOperationMove.ts");

  // 既有通路：{ root, path, action } —— path 是相对项目根的路径。
  assert.match(hooks, /body: JSON\.stringify\(\{ root, path, action, name \}\)/);
  assert.match(move, /action: "move", destination: dirs\.path/);
  assert.match(hooks, /confirmDialog/);
  assert.match(hooks, /promptDialog/);
});

test("file operation menu label exists in both locales", async () => {
  const zh = read("src/i18n/locales/zh-CN-projects.ts");
  const en = read("src/i18n/locales/en-US-projects.ts");
  assert.match(zh, /"fileOperation\.menuFor":/);
  assert.match(en, /"fileOperation\.menuFor":/);
});

console.log("file-entry-row-menu.test.mjs: OK");
