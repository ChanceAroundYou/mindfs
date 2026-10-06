// 会话窗口拉取的 in-flight 去重（2026-10-07）。
//
// 症状（实测）：打开一个会话时，每个 sessionKey 在 `?latest=20` 的访问日志里
// **恰好出现两次**，两次都返回 200 —— 同一份几百 KB 的载荷传了两遍，这是
// 「打开会话慢」最直接的一刀。
//
// 契约：同 (nodeId, rootId, sessionKey, beforeSeq, latest, limit) 的并发调用
// 共享同一个 Promise，只发一次 HTTP；不同 nodeId / 不同参数必须各自发。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const SRC = path.resolve(import.meta.dirname, "../src");

function loadSessionService() {
  const file = path.resolve(SRC, "services/session.ts");
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
      if (base === "base") {
        return { appURL: (p) => `http://x${p}`, appPath: (p) => p, wsURL: () => "ws://x" };
      }
      if (base === "authGate") return { currentUser: () => null };
      if (base === "rootNode") return { getRootNodeId: () => "node-A" };
      if (base === "scope") return { scopeSessionKey: (a, b) => `${a}::${b}` };
      if (base === "api") {
        return {
          protectedFetch: async () => new Response("{}", { status: 200 }),
          // 关键桩：手动控制何时 resolve，才能造出「两个并发调用都在途」的窗口。
          protectedJSON: (url) => {
            calls.push(url);
            return new Promise((resolve) => {
              pendingResolvers.push(() =>
                resolve({ session: { key: "s1", exchanges: [] }, window_meta: { total: 1 } }),
              );
            });
          },
          withNodeRetry: (fn) => fn(),
        };
      }
      if (base === "e2ee") {
        return {
          e2eeService: {
            decodeWSMessage: (m) => m,
            encodeWSMessage: (m) => m,
            ensureSession: async () => {},
            handleServerError: () => {},
            hasSecret: () => false,
            isRequired: () => false,
            setClientId: () => {},
            wsProofParams: () => ({}),
          },
        };
      }
      throw new Error(`unexpected require: ${name}`);
    },
    console: { error: () => {}, warn: () => {}, log: () => {} },
    URLSearchParams,
    Response,
    fetch: async () => new Response("{}", { status: 200 }),
    AbortController,
    setTimeout,
    clearTimeout,
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return {
    service: sandbox.exports.sessionService,
    calls,
    release: () => {
      const pending = pendingResolvers.splice(0);
      pending.forEach((resolve) => resolve());
    },
  };
}

const { service, calls, release } = loadSessionService();
assert.ok(service, "sessionService 必须导出");

// ── 并发同参：只发一次 ──
const p1 = service.getSessionWindow("root1", "s1", { latest: 20, limit: 20 });
const p2 = service.getSessionWindow("root1", "s1", { latest: 20, limit: 20 });
await Promise.resolve();
assert.equal(calls.length, 1, "并发同参必须只发一次 HTTP（双击/双 effect 是实测症状）");
release();
const [r1, r2] = await Promise.all([p1, p2]);
assert.equal(r1?.session?.key, "s1");
assert.equal(r2?.session?.key, "s1", "第二个调用必须拿到同一份结果");

// ── 串行同参：第一次结算后必须能再发（去重不能变成缓存）──
const p3 = service.getSessionWindow("root1", "s1", { latest: 20, limit: 20 });
await Promise.resolve();
assert.equal(calls.length, 2, "在途清空后同参必须重新发请求，否则永远拿不到新数据");
release();
await p3;

// ── 不同参数：必须各自发 ──
const pa = service.getSessionWindow("root1", "s1", { latest: 20 });
const pb = service.getSessionWindow("root1", "s1", { latest: 20, beforeSeq: 100 });
const pc = service.getSessionWindow("root2", "s1", { latest: 20 });
await Promise.resolve();
assert.equal(calls.length, 5, "beforeSeq / rootId 不同是不同请求，不能被去重顶掉");
release();
await Promise.all([pa, pb, pc]);

console.log("session-window-dedup: ok");
