import assert from "node:assert/strict";
import { test } from "vitest";

// 分片缓存命中判据必须比较「该 seq 的原值引用」。曾经每次调用都现包
// `{ [seq]: blocks[seq] }` 再和新包的对象做 `!==`，恒不相等 —— 缓存永不命中，
// 每个流式事件都把整份时间线重算一遍（排序 + 拼文本 + 哈希 id）。
const { createTimelineChunkCache } = await import("../src/hooks/sessionTimelineCache.ts");

const T0 = "2026-11-25T00:00:00.000Z";

function exchange(seq, content = "") {
  return { seq, role: "assistant", content, timestamp: T0 };
}

function textBlock(seq, pos, text) {
  return { seq, pos, type: "text", text };
}

test("同一 exchange + 同一 blocks 引用 → 返回同一 items 数组", () => {
  const cache = createTimelineChunkCache();
  const ex = exchange(1);
  const blocks = { "1": [textBlock(1, 0, "hello")] };
  const aux = { "1": [] };

  const first = cache.itemsFor(ex, aux, blocks, 0);
  cache.resetTouched();
  const second = cache.itemsFor(ex, aux, blocks, 0);

  assert.equal(second, first, "未变化的持久分片必须直接复用（引用相等）");
});

test("命中也要算 touched：retainUntouched 不能把复用的分片淘汰", () => {
  const cache = createTimelineChunkCache();
  const ex = exchange(1);
  const blocks = { "1": [textBlock(1, 0, "hello")] };
  const aux = { "1": [] };

  const first = cache.itemsFor(ex, aux, blocks, 0);
  cache.resetTouched();
  const second = cache.itemsFor(ex, aux, blocks, 0);
  assert.equal(second, first);

  // 模拟一次完整增量构建的收尾：本轮出现过的键必须保留。
  cache.retainUntouched();
  const third = cache.itemsFor(ex, aux, blocks, 0);
  assert.equal(third, first, "命中后 retainUntouched 不得淘汰该分片");
});

test("只换一个 seq 的 blocks → 只有该分片重建", () => {
  const cache = createTimelineChunkCache();
  const ex1 = exchange(1);
  const ex2 = exchange(2);
  const blocks1 = { "1": [textBlock(1, 0, "one")], "2": [textBlock(2, 0, "two")] };
  const aux = { "1": [], "2": [] };

  const one = cache.itemsFor(ex1, aux, blocks1, 0);
  const two = cache.itemsFor(ex2, aux, blocks1, 1);

  cache.resetTouched();
  const blocks2 = {
    "1": [textBlock(1, 0, "one changed")],
    "2": blocks1["2"],
  };
  const oneAgain = cache.itemsFor(ex1, aux, blocks2, 0);
  const twoAgain = cache.itemsFor(ex2, aux, blocks2, 1);

  assert.notEqual(oneAgain, one, "blocks 引用变化的 seq 需要重建");
  assert.equal(twoAgain, two, "未变化的 seq 必须复用自己的分片");
});

test("瞬时行按对象身份命中；对象换了就重建", () => {
  const cache = createTimelineChunkCache();
  const transient = { seq: 0, role: "agent", content: "draft", timestamp: T0 };

  const first = cache.itemsFor(transient, {}, {}, 0);
  const again = cache.itemsFor(transient, {}, {}, 0);
  assert.equal(again, first, "同一个瞬时对象必须复用分片");

  const next = { ...transient, content: "draft + more" };
  const rebuilt = cache.itemsFor(next, {}, {}, 0);
  assert.notEqual(rebuilt, first, "瞬时对象换了（内容更新）必须重建");
});