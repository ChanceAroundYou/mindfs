import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("session-viewer-scroll-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const viewer = readPhysical("src/components/session/SessionViewer.tsx");
const scroll = readPhysical("src/components/session/useSessionViewerScroll.ts");
const windowing = readPhysical("src/components/session/useSessionWindowing.ts");
const timelineView = readPhysical("src/components/session/SessionViewerTimeline.tsx");
const meta = readPhysical("src/components/session/buildTimelineItemMeta.ts");
const styles = readPhysical("src/components/session/sessionViewerStyles.ts");

assert.ok(viewer.split("\n").length - 1 < 500, "the physical viewer stays below the hard line");
assert.match(viewer, /useSessionViewerScrollState\(activeScrollRef\)/);
assert.match(viewer, /useSessionViewerScrollPositionEffects\(/);
assert.match(viewer, /useSessionViewerStickinessEffect\(/);
assert.match(viewer, /useTargetSeqScroll\([\s\S]*?targetSeqScrollKeyRef[\s\S]*?cancelTargetSeqScroll/);
assert.match(scroll, /STICK_REARM_PX/);
assert.match(scroll, /movedUp/);
assert.match(scroll, /refreshCurrentUserMessageIndex\(\)/);
assert.match(meta, /for \(let i = 0; i < timeline\.length; i \+= 1\)/);
assert.match(viewer, /<style>\{sessionViewerAnimationStyles\}<\/style>/);
assert.match(styles, /@keyframes sessionLoadingPulse/);

// ── 2026-10-27 回归：按钮显隐与 scroll 监听 ───────────────────────────
// 事故：b24dbee3 加手势监听时把 addEventListener("scroll") 删了但 removeEventListener 留着，
// 于是按钮只能由「只会置真」的三个手势点亮，滚轮下到底不消失、拖滚动条上滑不出现。
assert.match(
  scroll,
  /el\.addEventListener\("scroll", updateStickiness/,
  "scroll 监听必须存在：它是按钮显隐的唯一输入",
);
assert.match(scroll, /el\.removeEventListener\("scroll", updateStickiness\)/);
assert.match(
  scroll,
  /setShowJumpToLatest\(distanceFromBottom > STICK_REARM_PX\)/,
  "按钮显隐必须由实际离底距离决定，不能与 shouldStickToBottomRef 混用",
);
// 手势只让位，不再各自点亮按钮（否则又变成多真相源）。
{
  const gestureBlock = scroll.slice(scroll.indexOf("const onWheel"));
  assert.doesNotMatch(
    gestureBlock,
    /setShowJumpToLatest\(true\)/,
    "手势不应直接置按钮可见：显隐只由位置读数决定",
  );
}

// ── 2026-10-27 回归：到底按钮逐帧收敛 ─────────────────────────────────
// 一次 smooth 会被虚拟库的即时 scrollTop 写入打断，停在半路。
assert.match(scroll, /requestAnimationFrame\(step\)/, "到底必须逐帧收敛");
assert.match(scroll, /MAX_FRAMES/, "收敛必须有帧数上限");

// ── 2026-10-27 回归：前插按元素矩形锚定 ───────────────────────────────
// 虚拟化下 scrollHeight 是估算高度，前插后测量修正不会在后退方向补偿，
// 所以只能用「同一行元素的真实矩形」当锚，多帧收敛。
assert.match(timelineView, /data-timeline-key=\{String\(row\.key\)\}/, "虚拟行必须携带稳定的锚定键");
assert.match(windowing, /querySelectorAll<HTMLElement>\("\[data-timeline-key\]"\)/, "loadMore 必须找视口内锚点行");
assert.match(windowing, /getBoundingClientRect\(\)\.top - anchorTop/, "前插必须按真实矩形回补");

// ── 2026-10-27 回归：一次上滑只翻一页（失控连翻到底）─────────────────
// 事故：锚行有 key 但前插后被推出 overscan 永不挂载时，旧代码「有 anchorKey 走矩形、
// 无 anchorKey 走 scrollHeight」两条分支都不成立，一个字节都不写 → scrollTop 停在 0 →
// 顶部哨兵持续 intersecting → 每重建一次 observer 就点着下一页（实测一次手势连发 29 次
// 请求、冲完 240 条并停在顶部）。
assert.match(
  windowing,
  /isPrependingRef\.current \|\| !windowMeta\.hasMore/,
  "loadMore 必须在上一页回补期间拒收新请求，否则哨兵会连着点下一页",
);
{
  // 锚行未挂载时必须退回 scrollHeight 差值，不能「有 key 就只走矩形」。
  const settleBlock = windowing.slice(windowing.indexOf("const settle = () =>"));
  assert.match(
    settleBlock,
    /const delta = row\s*\n?\s*\? /,
    "锚行在不在都要算得出 delta：缺失时必须退回 scrollHeight 差值",
  );
  assert.doesNotMatch(
    settleBlock,
    /else if \(frames < 2\)/,
    "锚行缺失不能只重试两帧就放弃（那时它还没挂载回来）",
  );
  assert.doesNotMatch(
    settleBlock,
    /if \(!anchorKey\) \{\s*\n\s*el\.scrollTop = el\.scrollHeight - anchor/,
    "scrollHeight 回补不能只在没有 anchorKey 时才走",
  );
}

// ── 2026-10-27 回归：窗口态必须按会话重建 ─────────────────────────────
// 事故：windowMeta 跨会话残留，`applyWindow(keepOlder)` 取 `min(旧 minSeq, 新 minSeq)`，
// 从 seq 起于 1 的会话切到起于 233 的会话后 minSeq 被钉在 1，`loadMore` 恒发
// `beforeSeq=1` → 服务端回空 → `res` 为 null 直接返回 → **上翻一页也加载不出来**。
// 顺序也有要求：必须在发出尾部窗口请求之前重置，否则 keepOlder 先吃到旧值。
{
  const resetAt = windowing.indexOf("setWindowMeta({ total: 0, hasMore: false, minSeq: 0, maxSeq: 0 });");
  const initAt = windowing.indexOf('getSessionWindow(rootId || "", sessionKey, { latest: SESSION_WINDOW_SIZE');
  assert.ok(resetAt > 0, "切换会话时必须重置 windowMeta");
  assert.ok(initAt > 0, "初始化窗口拉取必须存在");
  assert.ok(resetAt < initAt, "windowMeta 必须在拉取尾部窗口之前重置");
}

console.log("session-viewer-scroll-split.test.mjs: OK");
});
