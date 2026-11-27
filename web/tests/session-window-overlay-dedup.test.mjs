import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

test("session-window-overlay-dedup", async () => {
// 2026-09-17 实测 bug：ask 触发后，ask 上下各渲染一块同样的分析文本，重开会话必现。
// 根因是「方案 C 重锚定」把 App 的整份缓存（持久化行 + 直播瞬时行 seq=0）当成窗口喂给
// applyWindow，瞬时行于是同时进了窗口态和 overlay 两条来源；而 buildBaseTimeline 对
// thought/正文没有 callId 可去重（dedupeToolCards 只管工具卡），就被渲染两遍。
const viewer = readFileSync(
  new URL("../src/components/SessionViewer.tsx", import.meta.url),
  "utf8",
);
const projection = readFileSync(
  new URL("../src/components/session/useSessionViewerExchangeProjection.ts", import.meta.url),
  "utf8",
);

// ── 1. 合并规则收在纯模块导出函数里（2026-11-27 起）──────────────────
// 组件只负责调用；规则本身见 src/shared/sessionOverlay.ts。
assert.match(
  viewer,
  /const composedExchanges = useMemo\(\s*\(\) => composeWindowAndOverlay\(visibleExchanges/,
  "SessionViewer 必须把窗口/overlay 合并交给 composeWindowAndOverlay",
);
const { composeWindowAndOverlay: compose } = await import("../src/shared/sessionOverlay.ts");

// 正常情形：窗口 4 行 + overlay 补 2 行尾巴
const winRows = [{ seq: 1 }, { seq: 2 }, { seq: 3 }, { seq: 4 }];
const overlayRows = [{ seq: 0, role: "thought", content: "t" }, { seq: 0, role: "agent", content: "a" }];
assert.deepEqual(
  compose(winRows, overlayRows),
  [...winRows, ...overlayRows],
  "disjoint window/overlay must compose in window-then-overlay order",
);

// 故障情形：重锚定把整份缓存（含瞬时行）装进窗口，overlay 又给了同一批对象
const shared = [{ seq: 0, role: "thought", content: "dup" }];
assert.deepEqual(
  compose(winRows, shared),
  [...winRows, ...shared],
  "an overlay row not present in the window is still appended",
);
const leaked = [...winRows, ...shared];
assert.deepEqual(
  compose(leaked, shared),
  leaked,
  "an overlay row already present in the window must not be appended twice",
);
assert.equal(
  compose(leaked, shared).filter((e) => e === shared[0]).length,
  1,
  "the shared exchange object must render exactly once",
);

// 故障情形（2026-11-27 实测）：窗口与缓存各持一份**不同对象**的同一 seq。
// blocks 以 seq 为键，两份都进时间线时同一段思考/正文渲染两遍（尾部 598 字思考块）。
const cachedCopy = [{ seq: 2, content: "b" }];
assert.deepEqual(
  compose([{ seq: 1, content: "a" }, { seq: 2, content: "b" }], cachedCopy),
  [{ seq: 1, content: "a" }, { seq: 2, content: "b" }],
  "同一 seq 的不同对象必须只保留窗口那份",
);

// ── 2. 重锚定只许把持久化行装进窗口态 ────────────────────────────────
assert.match(viewer, /keepPersistedExchanges\(Array\.isArray\(\(session as any\)\?\.exchanges\)/);
assert.match(
  projection,
  /exchanges\.filter\(\(exchange\) => isPersistedSeq\(\(exchange as \{ seq\?: unknown \}\)\?\.seq\)\)/,
  "the re-anchor payload must drop seq=0 transient rows before installing a window",
);

console.log("session-window-overlay-dedup: ok");
});
