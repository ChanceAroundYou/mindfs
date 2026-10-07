import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// 抽屉 pending 对账：任务结束后输入框仍显示停止符号、查看器仍「正在思考」的回归。
// 背景：会话的 pending 只由 WS session.done（handleSessionStreamDone）清除。断连/重绑
// 竞态会让那条事件丢失，抽屉/选中/缓存三处的 pending 永久卡在 true。会话列表不会卡：
// 它的蓝灯每 5s 从 /api/replying-sessions 对账一次。修法是把同一条对账补到另外三处
// （clearStalePending + App.tsx 里依赖 multiProjectPendingByKey 的 effect）。
//
// 纯逻辑测试，不依赖浏览器/网络：只加载 appSession.ts 里的纯函数。

function loadModule(relPath, exportNames, requireStub) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: requireStub };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const { clearStalePending } = loadModule(
  "src/app/appSession.ts",
  ["clearStalePending"],
  (id) => {
    if (id.includes("scope")) return { sessionKeyNodeId: () => "" };
    if (id.includes("appTask")) return { normalizeFastService: (v) => v };
    return {};
  },
);

const notReplying = () => false; // 服务端说「不在跑」
const replying = () => true; // 服务端说「在跑」

// ── 1. 核心：服务端说不在跑 → 清 pending ────────────────────────────────
{
  const drawer = { key: "s1", root_id: "r1", pending: true, exchanges: [] };
  const next = clearStalePending(drawer, notReplying);
  assert.notEqual(next, drawer, "有变化时必须返回新对象（调用方靠 !== 判断是否 setState）");
  assert.equal(next.pending, false, "服务端说不在跑 → pending 清成 false");
  assert.equal(next.key, "s1", "其余字段原样保留");
  assert.equal(next.exchanges.length, 0, "exchanges 等字段不能丢");
}

// ── 2. 服务端说在跑 → 不动（返回同引用）────────────────────────────────
{
  const drawer = { key: "s1", root_id: "r1", pending: true };
  const next = clearStalePending(drawer, replying);
  assert.equal(next, drawer, "服务端说在跑 → 原样返回，不触发无谓 setState");
}

// ── 3. 本来就没在跑 → 不动 ────────────────────────────────────────────
{
  const drawer = { key: "s1", root_id: "r1", pending: false };
  assert.equal(clearStalePending(drawer, notReplying), drawer, "pending:false 不动");
  const none = { key: "s1", root_id: "r1" };
  assert.equal(clearStalePending(none, notReplying), none, "pending 缺失不动");
}

// ── 4. 空值 / 缺键 → 不动 ──────────────────────────────────────────────
{
  assert.equal(clearStalePending(null, notReplying), null, "null 原样返回");
  assert.equal(clearStalePending(undefined, notReplying), undefined, "undefined 原样返回");
  const noKey = { pending: true };
  assert.equal(clearStalePending(noKey, notReplying), noKey, "缺 key 不动（同引用返回）");
}

// ── 5. 按 key 判定：只清服务端说不在跑的那一个 ──────────────────────────
{
  const live = new Set(["s2"]);
  const isReplying = (key) => live.has(key);
  const stale = { key: "s1", pending: true };
  const active = { key: "s2", pending: true };
  assert.equal(clearStalePending(stale, isReplying).pending, false, "s1 不在跑 → 清");
  assert.equal(clearStalePending(active, isReplying).pending, true, "s2 在跑 → 留");
}

// ── 6. 源码守卫：App.tsx 必须真的把这条对账接进依赖 multiProjectPendingByKey 的 effect ──
// 光有 helper 不够 —— 没人调它就等于没修。这里钉住接线：effect 依赖
// multiProjectPendingByKey，且对抽屉 / 选中 / 缓存三处都调了 clearStalePending。
{
  const app = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/App.tsx"),
    "utf8",
  );
  assert.match(
    app,
    /useEffect\(\(\) => \{[\s\S]*?multiProjectPendingByKey[\s\S]*?clearStalePending\(drawer[\s\S]*?clearStalePending\(selected[\s\S]*?clearStalePending\(cached[\s\S]*?\}, \[multiProjectPendingByKey/,
    "App.tsx 必须有依赖 multiProjectPendingByKey 的 effect，对抽屉/选中/缓存三处调 clearStalePending",
  );
  // 对账的真值必须来自 multiProjectPendingByKey（/api/replying-sessions 的落点），
  // 不是本地 optimistic 状态 —— 否则丢 done 时两边一起错，对账等于没做。
  assert.match(
    app,
    /isServerPending = \(key: string\) =>\s*!!multiProjectPendingByKey\[rootSessionKey\(rootID, key\)\]/,
    "对账真值必须读 multiProjectPendingByKey（服务端 /api/replying-sessions 的落点）",
  );
}

console.log("drawer-pending-reconcile.test.mjs: OK");
