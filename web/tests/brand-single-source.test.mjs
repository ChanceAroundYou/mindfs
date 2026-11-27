import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("brand-derived artifacts are in sync with internal/brand/brand.json", () => {
  const res = spawnSync("python3", ["scripts/brand-sync.py", "--check"], {
    cwd: root,
    encoding: "utf8",
  });
  assert.equal(res.status, 0, `${res.stdout}\n${res.stderr}`);
  assert.match(read("web/src/shared/brand.ts"), /export const BRAND = \{/);
});

test("frontend display strings use the brand source instead of hardcoded MindFS", () => {
  const locales = [
    "web/src/i18n/locales/zh-CN-workspace.ts",
    "web/src/i18n/locales/en-US-workspace.ts",
    "web/src/i18n/locales/zh-CN-agents.ts",
    "web/src/i18n/locales/en-US-agents.ts",
    "web/src/i18n/locales/zh-CN-projects.ts",
    "web/src/i18n/locales/en-US-projects.ts",
  ];
  for (const rel of locales) {
    assert.ok(!read(rel).includes("MindFS"), `${rel} 仍有硬编码产品名，改用 {appName}`);
  }

  // 这些位置没有协议/桥对象名，出现 MindFS 字面量就说明漏回了品牌变量。
  const displayOnly = [
    "web/src/app/appMisc.ts",
    "web/src/components/file/FileTreeAboutDialog.tsx",
    "web/src/components/file/FileTreeHeaderOverlays.tsx",
    "web/src/renderer/viewCatalog.ts",
  ];
  for (const rel of displayOnly) {
    assert.ok(!read(rel).includes("MindFS"), `${rel} 仍有硬编码产品名，改用 BRAND`);
  }
  assert.ok(!read("web/src/services/platform/download.ts").includes('"MindFS/"'), "iOS 下载目录应使用 BRAND.name");
});

test("i18n injects appName automatically", () => {
  const i18n = read("web/src/i18n/index.tsx");
  assert.match(i18n, /appName: BRAND\.name/);
  assert.match(read("web/src/i18n/locales/zh-CN-workspace.ts"), /"app\.name": "\{appName\}"/);
});