// 自动重载观测器（`Scope: G-AO`）的行为测试 —— 直接 import `src/services/reloadObserver.ts`。
//
// 这一层必须测，因为它是**现场唯一的证据来源**：它要是静默失效（存储抛异常被吞、
// 计数从 1 重来、采样撑爆 sessionStorage），以后所有人都会拿一份假数据下结论。
import { register } from "node:module";
import assert from "node:assert/strict";
import test from "node:test";

register("./ts-module-hook.mjs", import.meta.url);

const { installReloadObserver, readReloadReport } = await import(
  "../src/services/reloadObserver.ts"
);

/** 够用的假 window：只需存储、performance、事件注册三件。 */
function fakeWindow({ heap, navType = "reload" } = {}) {
  const storage = new Map();
  const listeners = new Map();
  const win = {
    sessionStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, value),
    },
    performance: {
      getEntriesByType: () => [{ type: navType }],
      ...(heap === undefined ? {} : { memory: { usedJSHeapSize: heap } }),
    },
    addEventListener: (type, handler) => listeners.set(type, handler),
  };
  return {
    win,
    storage,
    fire: (type, event) => listeners.get(type)?.(event),
    raw: () => JSON.parse(storage.get("mindfs.reloadObserver")),
  };
}

test("首次加载：loads=1，记下导航类型与堆占用", () => {
  const app = fakeWindow({ heap: 80 * 1024 * 1024, navType: "navigate" });
  const dispose = installReloadObserver(app.win);
  try {
    const report = app.raw();
    assert.equal(report.loads, 1);
    assert.equal(report.samples.length, 1);
    assert.equal(report.samples[0].navType, "navigate");
    assert.equal(report.samples[0].peakHeapMB, 80);
    assert.equal(report.lastUnloadAt, null);
  } finally {
    dispose();
  }
});

test("重载累加：同一标签页第二次加载 loads=2，且保留上一轮的堆峰值", () => {
  const app = fakeWindow({ heap: 100 * 1024 * 1024, navType: "navigate" });
  installReloadObserver(app.win)();
  const app2 = { ...app, win: { ...app.win, performance: { getEntriesByType: () => [{ type: "reload" }], memory: { usedJSHeapSize: 300 * 1024 * 1024 } } } };
  const dispose = installReloadObserver(app2.win);
  try {
    const report = app.raw();
    assert.equal(report.loads, 2);
    assert.deepEqual(
      report.samples.map((s) => s.navType),
      ["navigate", "reload"],
    );
    // 新一条沿用上一轮的峰值起点，避免「上一轮崩在高位」这条信息在重载后丢失。
    assert.equal(report.samples[1].peakHeapMB, 300);
  } finally {
    dispose();
  }
});

test("峰值只增不减，且卸载时落盘", () => {
  const app = fakeWindow({ heap: 50 * 1024 * 1024 });
  const dispose = installReloadObserver(app.win);
  try {
    app.fire("beforeunload");
    const report = app.raw();
    assert.equal(report.samples[0].peakHeapMB, 50);
    assert.ok(typeof report.lastUnloadAt === "number" && report.lastUnloadAt > 0);
  } finally {
    dispose();
  }
});

test("错误只记第一条，且截断——不把堆栈写进 sessionStorage", () => {
  const app = fakeWindow();
  const dispose = installReloadObserver(app.win);
  try {
    app.fire("error", { message: "b".repeat(500) });
    app.fire("error", { message: "第二条不该覆盖" });
    const recorded = app.raw().samples[0].lastError;
    assert.ok(recorded.startsWith("error: "), "错误要带来源前缀");
    assert.ok(recorded.length <= 200, `错误文本必须截断，实得 ${recorded.length}`);
    assert.ok(!recorded.includes("第二条"), "只保留本轮第一条错误");
  } finally {
    dispose();
  }
});

test("采样上限 10 条：观测器自己不能变成无界增长源", () => {
  const app = fakeWindow();
  for (let i = 0; i < 12; i++) {
    installReloadObserver(app.win)();
  }
  const report = readReloadReport(app.win);
  assert.equal(report.loads, 12, "loads 是计数器，不受采样上限影响");
  assert.equal(report.samples.length, 10, "samples 必须有上限");
  assert.equal(report.samples.at(-1).loads, 12);
});

test("存储不可用（隐私模式）时不抛异常，返回内存里那份", () => {
  const broken = {
    get sessionStorage() {
      throw new DOMException("denied", "SecurityError");
    },
    performance: { getEntriesByType: () => [] },
    addEventListener: () => {},
  };
  const dispose = installReloadObserver(broken);
  try {
    const report = broken.__mindfsReloadReport();
    assert.equal(report.loads, 1, "拿不到存储也要有一份可读的报告");
    assert.equal(readReloadReport(broken), null, "读不到就说读不到，不要伪造");
  } finally {
    dispose();
  }
});

test("存储里是垃圾时不炸，按首次加载处理", () => {
  const app = fakeWindow();
  app.storage.set("mindfs.reloadObserver", "{不是 JSON");
  const dispose = installReloadObserver(app.win);
  try {
    assert.equal(app.raw().loads, 1);
    assert.equal(app.raw().samples.length, 1);
  } finally {
    dispose();
  }
});
