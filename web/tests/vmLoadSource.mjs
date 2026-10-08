import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

export function loadSourceModule(relativeSourcePath, parentSandbox, cache = new Map()) {
  const file = path.resolve(import.meta.dirname, "../src", relativeSourcePath);
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
      const base = name.split("/").pop();
      const candidate = path.resolve(path.dirname(file), name);
      for (const ext of [".ts", ".tsx", ".js"]) {
        if (fs.existsSync(candidate + ext)) return loadSourceModule(path.relative(path.resolve(import.meta.dirname, "../src"), candidate + ext), parentSandbox, cache);
      }
      return parentSandbox.require(name);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return module.exports;
}
