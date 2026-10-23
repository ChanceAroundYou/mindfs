import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 时间线虚拟化（CUSTOM(G-JT)）契约。
//
// 起因：打开大会话时一页上千个时间线条目**同帧**挂载 —— 真机实测 DOM 941 → 16,824、
// 主线程一次 1.4–1.9 s 长任务。窗口预算（G-JS）压的是载荷，虚拟化压的是挂载数。
//
// 这里钉住的是「哪些东西绝不能因为虚拟化而丢掉」——它们都是历史上真实踩过的坑，
// 不是假想的边界：
//   ① 未答 ask 卡必须常驻（草稿在它的 DOM 里，滚走即丢）；
//   ② 屏外条目不在 DOM 后，用户消息序号与 targetSeq 定位不能再靠 querySelector；
//   ③ 少条目会话必须仍走全量渲染（虚拟化对小列表是净损失）；
//   ④ 条目 key 必须跨上翻前插稳定（否则测量缓存失效、翻页跳一下）。
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const hook = read("src/components/session/useSessionTimelineVirtualizer.ts");
const timeline = read("src/components/session/SessionViewerTimeline.tsx");
const viewer = read("src/components/session/SessionViewer.tsx");
const summaries = read("src/components/session/useUserMessageSummaries.ts");
const targetSeq = read("src/components/session/useTargetSeqScroll.ts");

test("virtualization: pending ask cards stay mounted", () => {
  // 未答 ask 卡必须进 rangeExtractor 的常驻集合 —— 否则用户正在填的表单会被滚走。
  assert.match(hook, /pendingAsk/);
  // 必须是**生效的** rangeExtractor 选项（写成 xxxExtractor 之类就不再被库读取）。
  assert.match(hook, /^\s*rangeExtractor: \(range\) =>/m);
  assert.match(hook, /\[\.\.\.defaultRangeExtractor\(range\), \.\.\.seqToIndex\.pendingAsk\]/);
  assert.match(hook, /"ask_user"/);
});

test("virtualization: small timelines stay fully rendered", () => {
  // 阈值以下不启用：条目少时全量渲染更快，也没有测量抖动。
  assert.match(hook, /TIMELINE_VIRTUALIZE_MIN_ITEMS/);
  assert.match(hook, /timeline\.length >= TIMELINE_VIRTUALIZE_MIN_ITEMS/);
  // 未启用时 virtual 必须是 null，渲染器才会走 map 分支。
  assert.match(hook, /virtual: virtualized/);
  assert.match(timeline, /if \(!virtual\)/);
  assert.match(timeline, /timeline\.map\(\(item, idx\) =>/);
});

test("virtualization: item keys are stable across prepend", () => {
  // key 里必须带条目 id（= seq:pos），不能只用索引：上翻前插会让索引整体后移，
  // 用索引当 key 等于让测量缓存对错行。
  assert.match(hook, /getItemKey: \(i\) => `\$\{sessionKey \|\| ""\}:\$\{timeline\[i\]\?\.id \|\| i\}`/);
});

test("virtualization: navigation no longer depends on off-screen DOM", () => {
  // 用户消息序号：虚拟化时走索引，不做 querySelectorAll（屏外节点根本不存在）。
  assert.match(summaries, /navigation\?\.virtualized/);
  assert.match(summaries, /navigation\.currentUserIndex\(\)/);
  assert.match(summaries, /navigation\.scrollToUserIndex\(index\)/);
  // targetSeq 跳转同理。
  assert.match(targetSeq, /navigation\?\.virtualized/);
  assert.match(targetSeq, /navigation\.scrollToSeq\(targetSeq\)/);
  // 两条路径都必须由 SessionViewer 接到同一个虚拟器实例（同一份索引）。
  assert.match(viewer, /useSessionTimelineVirtualizer\(\{/);
  assert.match(viewer, /navigation: timelineNav/);
});

test("virtualization: viewport container keeps a stable total height", () => {
  // 容器高度 = 虚拟总高度，否则滚动条长度会随渲染条目数变化（翻页时跳动）。
  assert.match(timeline, /height: virtual\.totalSize/);
  assert.match(timeline, /overflowAnchor: "none"/);
  assert.match(timeline, /measureElement/);
});

test("virtualization: measured element carries the attribute the virtualizer reads", () => {
  // ⑤ 挂到 measureElement 上的节点必须带 `data-index`。
  // virtual-core 用 options.indexAttribute（缺省 "data-index"）读行号，读不到就返回 -1，
  // isIndexInRange(-1) 为假 → ref 与 ResizeObserver 两条测量路径全部跳过 → 行高永远停在
  // estimateSize，真实内容比分配的高度高，视觉上就是文字互相重叠。曾经写成
  // `data-timeline-index` 正是这个形态（2026-10-23 真机复现）。
  assert.match(timeline, /ref=\{virtual\.measureElement\}/);
  assert.match(timeline, /data-index=\{row\.index\}/);
  // 换成别的属性名不会让虚拟化报错，只会静默不测量 —— 所以这里钉死「不许再出现变体」。
  assert.doesNotMatch(timeline, /data-timeline-index/);
});
