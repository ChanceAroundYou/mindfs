// 置顶刷新的 in-flight 去重（2026-10-07）。
//
// 症状（实测）：每次切项目都打两个 `GET /api/pins`，20 分钟内 30 次。
// 根因是两个 effect 同时触发 —— App.tsx 依赖 currentRootId、SessionList.tsx
// 依赖 selectedRootId/selectedNodeId，切项目时两者同一次提交里都变。
//
// 契约：并发的 refreshPinsFromServer 共享同一个 Promise，只发一次；
// 第一次结算后再调用必须重新发（去重不是缓存）。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const SRC = path.resolve(import.meta.dirname, "../src");

function loadPins() {
  const file = path.resolve(SRC, "services/pins.ts");
  const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;

  const calls = [];
  const pendingResolvers = [];
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (name) => {
      const base = name.split("/").pop();
      if (base === "controlPlane") return { controlPath: (p) => `/page${p}` };
      if (base === "api") {
        return {
          protectedJSON: (url) => {
            calls.push(url);
            return new Promise((resolve) => {
              pendingResolvers.push(() => resolve({ projects: {}, sessions: {} }));
            });
          },
        };
      }
      throw new Error(`unexpected require: ${name}`);
    },
    console,
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return {
    pins: sandbox.exports,
    calls,
    release: () => {
      const pending = pendingResolvers.splice(0);
      pending.forEach((resolve) => resolve());
    },
  };
}

const { pins, calls, release } = loadPins();
assert.equal(typeof pins.refreshPinsFromServer, "function");

// ── 并发：只发一次 ──
const p1 = pins.refreshPinsFromServer();
const p2 = pins.refreshPinsFromServer();
await Promise.resolve();
assert.equal(calls.length, 1, "两个 effect 同一次提交触发时必须只发一次 /api/pins");
assert.ok(calls[0].endsWith("/api/pins"), "必须走 controlPath 打主节点");
release();
await Promise.all([p1, p2]);

// ── 结算后再调用：必须重新发 ──
const p3 = pins.refreshPinsFromServer();
await Promise.resolve();
assert.equal(calls.length, 2, "在途清空后必须重新发，否则切项目看不到新置顶");
release();
await p3;

console.log("pins-refresh-dedup: ok");
