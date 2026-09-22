import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 2026-09-17 实测 bug：ask 触发后，ask 上下各渲染一块同样的分析文本，重开会话必现。
// 根因是「方案 C 重锚定」把 App 的整份缓存（持久化行 + 直播瞬时行 seq=0）当成窗口喂给
// applyWindow，瞬时行于是同时进了窗口态和 overlay 两条来源；而 buildBaseTimeline 对
// thought/正文没有 callId 可去重（dedupeToolCards 只管工具卡），就被渲染两遍。
const viewer = readFileSync(
  new URL("../src/components/SessionViewer.tsx", import.meta.url),
  "utf8",
);

// ── 1. 组合出口必须按对象同一性去重（两来源共用同一个 exchange 对象时只渲染一次）──
const composeStart = viewer.indexOf("const composedExchanges = useMemo(");
const composeEnd = viewer.indexOf("const { timeline, isStreaming", composeStart);
assert.ok(composeStart >= 0 && composeEnd > composeStart, "composedExchanges block should be found");
const composeSrc = viewer
  .slice(composeStart, composeEnd)
  // 把 TS 类型断言剥掉，只留可求值的 JS
  .replace(/\bas\s+[A-Za-z_$][\w$.]*(?:\[\])?/g, "");

const compose = new Function(
  "visibleExchanges",
  "tailOverlay",
  // useMemo 在测试里退化成直接求值：只取箭头函数本体，丢掉依赖数组
  `${composeSrc
    .replace("const composedExchanges = useMemo(", "return (")
    .replace(/,\s*\[visibleExchanges,\s*tailOverlay\],?\s*\);\s*$/, ")();")};`,
);

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
  compose(winRows, shared) ,
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

// ── 2. 重锚定只许把持久化行装进窗口态 ────────────────────────────────
const anchorStart = viewer.indexOf("const anchorExchanges = (Array.isArray((session as any)?.exchanges)");
const anchorEnd = viewer.indexOf("applyWindow(", anchorStart);
assert.ok(anchorStart >= 0 && anchorEnd > anchorStart, "anchor payload filter should be found");
assert.match(
  viewer.slice(anchorStart, anchorEnd),
  /\.filter\(\(ex\) => Number\(\(ex as any\)\?\.seq \|\| 0\) > 0\)/,
  "the re-anchor payload must drop seq=0 transient rows before installing a window",
);

console.log("session-window-overlay-dedup: ok");
