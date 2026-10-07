// 测试预加载：把 `fs.readFileSync` 对「已登记逻辑模块」的读取重定向到 source-map。
//
// 目的：让 `web/tests/*.test.mjs` 里既有的源码守卫断言在文件被移动/拆分后**零改动**继续生效。
// 覆盖三种读法（实测都有效，前提是 patch 发生在 node:fs 的 ESM facade 建立之前）：
//   · `fs.readFileSync(...)`            —— namespace 导入
//   · `readFileSync(...)`               —— 具名导入
//   · 本地 helper `read("src/…")`       —— 内部仍走 fs.readFileSync
//
// 由 web/package.json 的 test 脚本用 `--import ./tests/source-map-hook.mjs` 挂载。
//
// 安全性：只有「路径在 web/ 下」且「命中 MODULES 键」才重定向，其余一律透传原始实现。
import path from "node:path";
import { createRequire } from "node:module";
import { MODULES, WEB_ROOT, toLogical } from "./source-map.mjs";

const require = createRequire(import.meta.url);
const fs = require("node:fs");
const originalReadFileSync = fs.readFileSync.bind(fs);

const hasOwn = Object.prototype.hasOwnProperty;

fs.readFileSync = function patchedReadFileSync(target, options) {
  const logical = toLogical(target);
  if (logical && hasOwn.call(MODULES, logical)) {
    return MODULES[logical]
      .map((file) => originalReadFileSync(path.join(WEB_ROOT, file), options))
      .join("\n");
  }
  return originalReadFileSync(target, options);
};
