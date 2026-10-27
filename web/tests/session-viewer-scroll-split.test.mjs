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
const loadingState = readPhysical("src/components/session/SessionViewerLoadingState.tsx");

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
  /isPrependingRef\.current \|\|[\s\S]{0,80}!armedRef\.current/,
  "loadMore 必须在上一页回补期间、以及「用户还没再往上滚」时都拒收新请求",
);
// 提前量提到半屏以后，回补完成时哨兵往往仍落在提前量之内，只靠 isPrependingRef 挡不住
// 页与页之间的间隙。必须有「收工上闸 → 再滚才重新武装」这一对。
assert.match(windowing, /armedRef\.current = false/, "前插收工必须上闸");
assert.match(windowing, /armedRef\.current = true/, "用户再往上滚必须能重新武装");
assert.match(windowing, /PREPEND_REARM_PX/, "重新武装需要位移阈值，否则一次轻微抖动就放行");
// 重新武装必须**重建 observer**：哨兵回补后一直停在提前量之内，intersection 没有变化，
// IO 不会再投递一次 —— 只把 ref 置真，上一页结束后再也点不动（实测：置于 259px 处再滚
// 也不加载）。判据是「epoch 进 observer effect 的依赖」+「重新武装时推进 epoch」。
{
  const observerEffect = windowing.slice(windowing.indexOf("new IntersectionObserver"));
  assert.match(
    observerEffect,
    /\}, \[windowMeta\.hasMore, loadMore, armEpoch\]\)/,
    "重新武装必须让哨兵 effect 重建 observer，否则 IO 不再投递",
  );
  const rearmBlock = windowing.slice(windowing.indexOf("const onScroll = () => {"));
  assert.match(rearmBlock, /setArmEpoch\(/, "重新武装时必须推进 epoch 以重建 observer");
}
{
  // 锚行未挂载时必须退回 scrollHeight 差值，不能「有 key 就只走矩形」。
  const settleBlock = windowing.slice(windowing.indexOf("const settleStep = () =>"));
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

// ── 2026-10-27 回归：补偿必须在**绘制之前**发生（闪一下的来源）─────────
// 事故：`setVisibleExchanges(...)` 之后直接 `requestAnimationFrame(settle)`。React 把这几个
// setState 排到下一帧，而 rAF 回调完全可以排在提交**之前** —— 那一帧读到旧 DOM，delta 为 0、
// 回补落空；新行则在下一帧一次性推上来，用户看到「闪一下 + 位置抖一下」。
// 修法：状态提交用 flushSync 同步落地，紧接着同步跑第一步回补，之后才交回 rAF 吸收测量修正。
assert.match(windowing, /import \{ flushSync \} from "react-dom"/, "前插提交必须用 flushSync 同步落地");
assert.match(
  windowing,
  /flushSync\(\(\) => \{\s*\n\s*setVisibleExchanges/,
  "flushSync 必须包住整批前插状态（只包一半仍会跨帧）",
);
{
  // 第一次回补必须是同步调用，不能又塞回 rAF。
  const afterCommit = windowing.slice(windowing.indexOf("flushSync(() => {"));
  assert.match(
    afterCommit,
    /if \(!settleStep\(\)\)/,
    "提交之后必须立刻同步跑一次回补（绘制之前），不能只排 rAF",
  );
  assert.ok(
    afterCommit.indexOf("if (!settleStep())") < afterCommit.indexOf("window.requestAnimationFrame(settle)"),
    "同步回补必须排在 rAF 收敛之前",
  );
  // ── 2026-10-27 回归：全屏闪烁（空视口被画出去）──────────────────────
  // 事故：flushSync 提交时时间线已经是「新页 + 旧页」，但虚拟器的 offset 还停在前插前的
  // scrollTop —— 它只由 scroll 事件驱动，而 scroll 事件要等浏览器渲染步骤才派发。于是这次
  // 提交按旧 offset 去 map 新数组：视口区间落在**刚插进来的最老一段**（实测 p-177-* 33 行
  // 全部在视口上方，覆盖率 0%），直到下一个 scroll 事件才换回原处。CPU 降速 12× 复现：
  // 两次提交同在 frame 170、相隔 608 ms，中间那 0% 覆盖被单独画了一帧。
  // 修法：同步回补写完 scrollTop 之后主动 dispatch 一次 scroll，让库在**同一任务内**重算
  // 区间并 flushSync 重渲染，中间态一次都不呈现（复测：scroll 写在前、commit 在后、pct=100）。
  assert.match(
    afterCommit,
    /container\.dispatchEvent\(new Event\("scroll"\)\)/,
    "同步回补之后必须主动派发 scroll，否则虚拟器仍按旧 offset 渲染出一帧空视口",
  );
  assert.ok(
    afterCommit.indexOf("if (!settleStep())") < afterCommit.indexOf('dispatchEvent(new Event("scroll"))'),
    "合成 scroll 必须在第一次同步回补（写 scrollTop）之后，否则虚拟器读到的还是旧 offset",
  );
}

// ── 2026-10-27 回归：上翻转圈不能占布局 ────────────────────────────────
// 事故：转圈是内容流里 padding 8px + 14px 的块，出现时把所有内容下推 ~30px、消失时推回来，
// 与前插回补叠加就是「加载时闪一下、位置晃一下」。改成 height:0 的 sticky 浮层：
// 视觉上贴视口顶，盒模型高度为零，出现/消失都不动任何行。
{
  const spinnerBlock = loadingState.slice(loadingState.indexOf("loadingMore ? ("));
  assert.match(spinnerBlock, /position: "sticky"/, "上翻转圈必须是浮层");
  assert.match(spinnerBlock, /height: 0/, "上翻转圈必须不占布局高度");
  assert.doesNotMatch(spinnerBlock, /padding: "8px 0"/, "转圈不能再用 padding 撑开文档流");
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
