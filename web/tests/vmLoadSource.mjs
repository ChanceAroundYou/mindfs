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
 * 列表（都是相对 src/ 的），命中即加载；嵌套目录（net/、prefs/…）由
 * loadSourceModule 按**导入方所在目录**继续往下找。
 */
export function resolveRelativeSource(name, sandbox, searchDirs = [""]) {
  const rel = name.replace(/^\.\//, "");
  for (const dir of searchDirs) {
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
 * `searchDirs` 描述**根模块**所在目录（session 系列模块都在 src/services）。
 */
export function withSourceFallback(sandbox, searchDirs = [""]) {
  const stub = sandbox.require;
  sandbox.require = (name) => {
    try {
      return stub(name);
    } catch (error) {
      if (!name.startsWith(".")) throw error;
      return resolveRelativeSource(name, sandbox, searchDirs);
    }
  };
  return sandbox;
}

export function loadSourceModule(relativeSourcePath, parentSandbox, cache = new Map()) {
  const file = path.resolve(SRC_ROOT, relativeSourcePath);
  if (cache.has(file)) return cache.get(file);
  const source = fs.readFileSync(file, "utf8");
  const compiled = ts
    .transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
    })
    // vm 里跑的是 Script，不是 ESM —— `import.meta` 是**解析期**语法错误，
    // 等到运行期再兜已经晚了。少数真实模块（net/prefix 等）会用它取自身 URL，
    // 这里按文件路径替换成等价的 file:// 串。
    .outputText.replace(/\bimport\.meta\.url\b/g, JSON.stringify(`file://${file}`))
    .replace(/\bimport\.meta\b/g, "{}");
  const exports = {};
  const module = { exports };
  cache.set(file, module.exports);
  // 子模块的相对导入以**它自己所在目录**为基准（net/、prefs/… 都在 services 下面）。
  const searchDirs = [path.dirname(path.relative(SRC_ROOT, file))];
  const sandbox = {
    ...parentSandbox,
    exports,
    module,
    require: (name) => {
      try { return parentSandbox.require(name); } catch {}
      return resolveRelativeSource(name, parentSandbox, searchDirs);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return module.exports;
}
