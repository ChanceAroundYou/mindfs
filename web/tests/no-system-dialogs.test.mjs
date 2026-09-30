import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const srcRoot = path.resolve(import.meta.dirname, "../src");

/**
 * 2026-09：window.confirm / window.alert / window.prompt 全部换成应用内弹窗
 * （services/dialog.ts + components/DialogHost.tsx）。本测试守住这条，
 * 免得以后新代码又顺手写回系统弹窗。
 */

const files = [];
(function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full);
    } else if (/\.tsx?$/.test(entry.name)) {
      files.push(full);
    }
  }
})(srcRoot);

const offenders = [];
for (const file of files) {
  if (file.endsWith(path.join("services", "dialog.ts"))) continue;
  const source = fs.readFileSync(file, "utf8");
  // 只看真实调用；注释里提到 window.confirm 是允许的（替换说明）。
  const stripped = source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
  const match = stripped.match(/\bwindow\.(confirm|alert|prompt)\s*\(/);
  if (match) {
    offenders.push(`${path.relative(srcRoot, file)}: window.${match[1]}`);
  }
}
assert.deepEqual(offenders, [], `系统弹窗不应再出现：\n${offenders.join("\n")}`);

// 弹窗服务本身要能编译成可用的三件套
const dialog = fs.readFileSync(path.join(srcRoot, "services/dialog.ts"), "utf8");
assert.match(dialog, /export function confirmDialog/);
assert.match(dialog, /export function promptDialog/);
assert.match(dialog, /export function alertDialog/);

// 宿主组件要挂在 App 上
const app = fs.readFileSync(path.join(srcRoot, "App.tsx"), "utf8");
assert.match(app, /import \{ DialogHost \}/);
assert.match(app, /<DialogHost \/>/);

// 常用按钮文案两种语言都要有
for (const locale of ["zh-CN.ts", "en-US.ts"]) {
  const text = fs.readFileSync(path.join(srcRoot, "i18n/locales", locale), "utf8");
  assert.match(text, /"common\.confirm"/, `${locale} 缺 common.confirm`);
  assert.match(text, /"update\.confirmInstall"/, `${locale} 缺 update.confirmInstall`);
}

console.log("no-system-dialogs source contracts OK");
