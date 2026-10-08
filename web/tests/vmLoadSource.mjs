import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const SRC_ROOT = path.resolve(import.meta.dirname, "../src");

/**
 * 沙箱里 stub 没覆盖到的相对模块（`./xxx`）→ 真实源码。
 *
 * 为什么不逐个列白名单：每拆出一个新子模块就要回来改一遍测试，那正是当初
 * 「session.ts 不能拆」的成因。`searchDirs` 给沙箱一个「相对路径基准目录」
 * （session 系列测试都是 services/ 下的兄弟模块），命中即加载。
 */
export function resolveRelativeSource(name, sandbox, searchDirs = ["services"]) {
  const rel = name.replace(/^\.\//, "");
  for (const dir of ["", ...searchDirs]) {
    for (const ext of ["", ".ts", ".tsx", ".js"]) {
      const candidate = path.resolve(SRC_ROOT, dir, rel + ext);
      if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) {
        return loadSourceModule(path.relative(SRC_ROOT, candidate), sandbox);
      }
    }
  }
  throw new Error(`unexpected require: ${name}`);
}

/**
 * 给既有沙箱装上「stub 优先、真源码兜底」的 require。
 * stub 行为完全不变（命中即返回），因此原有测试的隔离语义不动。
 */
export function withSourceFallback(sandbox) {
  const stub = sandbox.require;
  sandbox.require = (name) => {
    try {
      return stub(name);
    } catch (error) {
      if (!name.startsWith(".")) throw error;
      return resolveRelativeSource(name, sandbox);
    }
  };
  return sandbox;
}

export function loadSourceModule(relativeSourcePath, parentSandbox, cache = new Map()) {
  const file = path.resolve(SRC_ROOT, relativeSourcePath);
  if (cache.has(file)) return cache.get(file);
  const source = fs.readFileSync(file, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const exports = {};
  const module = { exports };
  cache.set(file, module.exports);
  const sandbox = {
    ...parentSandbox,
    exports,
    module,
    require: (name) => {
      try { return parentSandbox.require(name); } catch {}
      return resolveRelativeSource(name, parentSandbox);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return module.exports;
}
