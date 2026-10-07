// 让 `node --test` 能直接 import 仓库里的 `.ts` 源码。
//
// 背景：Node 22.18+ 原生剥离类型，但**要求说明符带扩展名**，而本仓库由 Vite 打包，
// 一律写 `import { x } from "./base"`。于是 `await import("../src/services/session.ts")`
// 会在它自己的第一条相对导入上炸（Cannot find module '.../services/base'）。
//
// 这不是新增测试框架 —— 只是补上扩展名解析，让「直接 import 源码跑纯函数」这条**仓库已有
// 的先例**（`tests/markdown-outline.test.mjs` → `src/components/markdownOutline.ts`、
// `tests/task-stage-panel.test.mjs` → `src/app/appTask.ts`）也能用在带运行时依赖的模块上。
// （注：`task-stage-panel.test.mjs:328` 的注释把前者写成了 `markdownOutline.test.mjs`，
// 文件名少了一处连字符，实际是 `markdown-outline.test.mjs`。）
//
// 用法（在测试文件顶部，必须在 await import 源码之前）：
//
//     import { register } from "node:module";
//     register("./ts-module-hook.mjs", import.meta.url);
//     const { isTransientExchange } = await import("../src/services/session.ts");
//
// 边界（别指望它万能）：
//   · `.tsx` 导入不了 —— JSX 需要真正的转换，Node 只剥类型。所以判定逻辑住在组件里的
//     模块（SessionViewer.tsx）测不了，得先把判定抽成 `.ts` 纯函数。
//   · 没有 `import type` 的纯类型导入（`import { Session } from "./x"` 里 Session 只是类型）
//     会在运行时找不到导出而报 SyntaxError。遇到就改用 `import type`。
import { existsSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { redirectSingleFileModule } from "./source-map.mjs";

/** 按优先级尝试的扩展名。 */
const EXTENSIONS = [".ts", ".tsx", "/index.ts", "/index.tsx", ".js", ".mjs", ".json"];

/** 解析移动后的物理文件：先试保留原扩展名，再试补扩展名。 */
function redirectTarget(redirected, keepExtension) {
  if (!redirected) return null;
  const candidates = keepExtension
    ? [redirected + keepExtension, ...EXTENSIONS.map((ext) => redirected + ext)]
    : [redirected + ".ts", redirected + ".tsx", redirected + "/index.ts", redirected + "/index.tsx"];
  for (const candidate of candidates) {
    if (existsSync(candidate)) return candidate;
  }
  return null;
}

export async function resolve(specifier, context, nextResolve) {
  const isRelative = specifier.startsWith("./") || specifier.startsWith("../");
  if (isRelative) {
    const base = new URL(specifier, context.parentURL).pathname;
    const hasExtension = /\.[a-z0-9]+$/i.test(specifier);
    // 先看这个逻辑模块是否已被 source-map 移动到别处（单文件移动）。
    // 带扩展名的导入（`../src/services/fileNavigation.ts`）也要走重定向 ——
    // 文件移动后原路径不存在，不重定向会 ERR_MODULE_NOT_FOUND。
    const keepExtension = hasExtension ? base.slice(base.lastIndexOf(".")) : "";
    const redirected = redirectTarget(
      redirectSingleFileModule(hasExtension ? base.slice(0, base.lastIndexOf(".")) : base),
      keepExtension,
    );
    if (redirected) {
      return { url: pathToFileURL(redirected).href, shortCircuit: true };
    }
    if (!hasExtension) {
      for (const ext of EXTENSIONS) {
        if (existsSync(base + ext)) {
          return { url: pathToFileURL(base + ext).href, shortCircuit: true };
        }
      }
    }
  }
  return nextResolve(specifier, context);
}
