// 源码守卫测试的「逻辑模块 → 物理文件」单一映射。
//
// 背景：`web/tests/*.test.mjs` 里有大量测试直接读 `src/**` 的源码原文做正则断言
// （`assert.match(app, /…/)`）。这些断言按**逻辑模块**写，而不是按物理文件写；
// 一旦某个巨文件被拆分、或某个文件被按领域归类移动，物理路径就变了。
//
// 这里把「逻辑路径（测试里一直沿用的、相对 web/ 的路径）→ 当前物理文件列表」
// 收敛成唯一一处。文件被移动或拆分时只改 `MODULES`，测试与断言都不用动。
//
// 消费方：Vitest tests use source-map-fs.mjs for logical source reads;
// vitest.config.ts resolves dynamic imports that target moved source modules.
//
// 注意：本文件刻意用 `createRequire` 取 `node:fs`，而不是 ESM `import fs from "node:fs"`。
// 因为 ESM 具名导入是在 `node:fs` 的 ESM facade 建立时快照的，而 facade 一旦建立，
// 之后再 patch CJS 导出就覆盖不到具名导入了（实测）。createRequire 不建立 facade。
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const fs = require("node:fs");
// 捕获原始实现：readSource 自身必须绕过 patch，否则会递归。
const originalReadFileSync = fs.readFileSync.bind(fs);

import { COMPONENT_MODULES } from "./source-map-modules-components.mjs";
import { SERVICE_MODULES } from "./source-map-modules-services.mjs";
import { APP_MODULES } from "./source-map-modules-app.mjs";

/** 仓库 web/ 根目录。 */
export const WEB_ROOT = path.resolve(import.meta.dirname, "..");

/**
 * 逻辑路径（相对 web/）→ 物理文件（相对 web/，可多个）。
 *
 * 只登记「被移动过」或「被拆分过」的逻辑模块；未登记的一律按原路径读取。
 * 维护规则：文件移动/拆分时改这里，测试不动。
 */
export const MODULES = {
  ...COMPONENT_MODULES,
  ...SERVICE_MODULES,
  ...APP_MODULES,
};

/** 逻辑路径对应的物理文件列表；未登记则返回原路径本身。 */
export function moduleFiles(logical) {
  return MODULES[logical] ?? [logical];
}

/** 按逻辑路径读取源码文本；拆分过的模块返回其全部物理文件的拼接。 */
export function readSource(logical) {
  return moduleFiles(logical)
    .map((file) => originalReadFileSync(path.join(WEB_ROOT, file), "utf8"))
    .join("\n");
}

/**
 * **未被 patch 过**的 readFileSync。
 *
 * 给少数「必须按物理文件判」的守卫用：它们断言的是「全仓只允许一个文件实现某把尺」，
 * 走 patch 后的读法会把 `src/services/session.ts` 的登记列表（已拼进
 * sessionExchangeMerge.ts 等）与它自己单独读的那份算成两处命中。
 *
 * 直接使用原始文件系统读取，避免逻辑路径映射改变物理文件扫描结果。
 */
export const rawReadFileSync = originalReadFileSync;

/**
 * 把测试里的绝对路径 / file URL 归一成相对 web/ 的逻辑路径（POSIX 分隔符）。
 * 不是 web/ 下的文件（或参数是 fd 等非路径）返回 null。
 */
export function toLogical(target) {
  try {
    let abs;
    if (target instanceof URL) {
      abs = fileURLToPath(target);
    } else if (typeof target === "string") {
      abs = path.resolve(target);
    } else {
      return null;
    }
    const rel = path.relative(WEB_ROOT, abs);
    if (!rel || rel.startsWith("..") || path.isAbsolute(rel)) {
      return null;
    }
    return rel.split(path.sep).join("/");
  } catch {
    return null;
  }
}

/**
 * 供 Vitest resolver 用：把「解析到逻辑路径、但物理文件已移动」的单文件模块
 * 重定向到新的物理路径（绝对、无扩展名）。多文件（被拆分的 barrel）不重定向 ——
 * 它们的入口 shim 仍在原路径，能被正常解析。
 */
const LOGICAL_EXTENSIONS = [".ts", ".tsx", "/index.ts", "/index.tsx", ".js", ".mjs", ".json"];

export function redirectSingleFileModule(absNoExt) {
  const rel = toLogical(absNoExt);
  if (!rel) return null;
  for (const ext of LOGICAL_EXTENSIONS) {
    const files = MODULES[rel + ext];
    if (files && files.length === 1) {
      return path.join(WEB_ROOT, files[0].replace(/\.[^.]+$/, ""));
    }
  }
  return null;
}
