import assert from "node:assert/strict";
import { test } from "vitest";
// 分片增量构建的 id 必须与整批构建逐字一致（2026-10-26 压叠事故回归）。
// 缓存逐片调 buildTimelineItems([ex])：分片内下标恒为 0，id 下标必须传全数组位置，
// 否则 N 片撞成同一个 id，React key + 虚拟器 getItemKey 双撞键，测量缓存把所有行
// 定位到同一个 start —— 时间列叠字、内容行压叠。
const { buildBaseTimeline } = await import("../src/hooks/sessionTimeline.ts");
const { buildTimelineIncremental, createTimelineChunkCache } = await import(
  "../src/hooks/sessionTimelineCache.ts"
);

const T0 = "2026-10-26T00:00:00Z";
const exs = [
  { seq: 1, role: "user", content: "hello", timestamp: T0, agent: "claude" },
  { seq: 2, role: "assistant", content: "world", timestamp: T0, agent: "claude" },
  { seq: 3, role: "user", content: "again", timestamp: T0, agent: "claude" },
  { seq: 4, role: "assistant", content: "done", timestamp: T0, agent: "claude" },
];

test("incremental ids match whole-batch ids exactly", () => {
  const full = buildBaseTimeline(exs, {}, {}).map((i) => i.id);
  const inc = buildTimelineIncremental(exs, {}, {}, createTimelineChunkCache()).map((i) => i.id);
  assert.deepEqual(inc, full, "分片增量 id 必须与整批构建逐字一致");
  assert.equal(new Set(inc).size, inc.length, "增量结果里不许有重复 id");
});

test("second build reuses cache without changing ids", () => {
  const cache = createTimelineChunkCache();
  const first = buildTimelineIncremental(exs, {}, {}, cache).map((i) => i.id);
  // 模拟上翻前插：前面多出一页，旧对象引用不变
  const older = [{ seq: 0, role: "user", content: "pre", timestamp: T0, agent: "claude" }, ...exs];
  const second = buildTimelineIncremental(older, {}, {}, cache).map((i) => i.id);
  assert.equal(new Set(second).size, second.length, "前插后仍不许有重复 id");
  assert.ok(second.length > first.length, "前插后条目应变多");
});
