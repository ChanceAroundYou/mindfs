import assert from "node:assert/strict";
import { test } from "vitest";

const { computeTailOverlay } = await import("../src/shared/sessionOverlay.ts");
const row = (seq, role, content, mindfs_id) => ({ seq, role, content, mindfs_id });

test("persisted rows are excluded only when the visible window contains their seq", () => {
  const visible = [row(1, "user", "ask", "turn-1"), row(2, "agent", "reply", "turn-1")];
  const cache = [
    ...visible,
    row(0, "agent", "live reply", "turn-2"),
    row(3, "user", "next ask", "turn-2"),
  ];
  assert.deepEqual(computeTailOverlay({ exchanges: cache, visibleExchanges: visible }), [
    cache[2],
    cache[3],
  ]);
});

test("a transient row yields to a persisted row with the same role category and mindfs_id", () => {
  const persisted = row(8, "agent", "persisted answer", "turn-8");
  const transientCopy = row(0, "assistant", "different text", "turn-8");
  const thoughtCopy = { role: "thought", mindfs_id: "turn-8", content: "thinking" };
  const out = computeTailOverlay({
    exchanges: [persisted, transientCopy, thoughtCopy],
    visibleExchanges: [persisted],
  });
  assert.deepEqual(out, []);
});

test("same text with a different mindfs_id remains visible", () => {
  const persisted = row(8, "agent", "okay", "turn-8");
  const repeated = row(0, "agent", "okay", "turn-9");
  assert.deepEqual(
    computeTailOverlay({ exchanges: [persisted, repeated], visibleExchanges: [persisted] }),
    [repeated],
  );
});

test("a repeated user message with a different mindfs_id remains visible", () => {
  const persisted = row(8, "user", "继续", "turn-8");
  const repeated = row(0, "user", "继续", "turn-9");
  assert.deepEqual(
    computeTailOverlay({ exchanges: [persisted, repeated], visibleExchanges: [persisted] }),
    [repeated],
  );
});

test("user and agent rows with the same turn id do not hide each other", () => {
  const persistedUser = row(8, "user", "same body", "turn-8");
  const liveAgent = row(0, "agent", "same body", "turn-8");
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [persistedUser, liveAgent],
      visibleExchanges: [persistedUser],
    }),
    [liveAgent],
  );
});

test("matching a persisted row in the cache also suppresses its transient copy outside the window", () => {
  const oldVisible = row(1, "user", "ask", "turn-1");
  const cachedPersisted = row(2, "agent", "reply", "turn-1");
  const transientCopy = row(0, "assistant", "reply", "turn-1");
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [oldVisible, cachedPersisted, transientCopy],
      visibleExchanges: [oldVisible],
    }),
    [cachedPersisted],
  );
});

// 复现（2026-11-22，llm-deploy 会话）：CLI 在 result 之后继续输出的续写行没有持久行可落，
// 一直留在缓存尾巴里。用户 06:54 再发一条时，持久化的 user 行被追加在这些 06:35 的行之后；
// overlay 若把它们当活尾巴整体挪到窗口之后，用户的新消息就会被顶到 assistant 的旧行上面。
test("transient rows older than the newest persisted user turn are not live tail", () => {
  const persistedReply = {
    seq: 196,
    role: "agent",
    content: "draft is being pulled back",
    mindfs_id: "turn-196",
    timestamp: "2026-11-22T06:33:11.365Z",
  };
  const staleThought = {
    seq: 0,
    role: "thought",
    content: "user asks what to compare",
    timestamp: "2026-11-22T06:35:35.000Z",
  };
  const staleTool = {
    seq: 0,
    role: "tool",
    content: "",
    timestamp: "2026-11-22T06:35:35.000Z",
    toolCall: { callId: "call-stale" },
  };
  const userAsk = {
    seq: 197,
    role: "user",
    content: "需要我对比什么?",
    mindfs_id: "turn-197",
    timestamp: "2026-11-22T06:54:52.438Z",
  };
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [persistedReply, staleThought, staleTool, userAsk],
      visibleExchanges: [persistedReply, userAsk],
    }),
    [],
  );
  // 复现路径的终点：SessionViewer 的组合是 [窗口..., overlay 尾巴...]。
  // overlay 清空后，用户消息就是渲染出来的最后一行 —— 旧行不会再被顶到它后面。
  const composed = [
    ...[persistedReply, userAsk],
    ...computeTailOverlay({
      exchanges: [persistedReply, staleThought, staleTool, userAsk],
      visibleExchanges: [persistedReply, userAsk],
    }),
  ];
  assert.deepEqual(composed.map((ex) => ex.seq), [196, 197]);
});

test("a persisted user row that lands after this turn's live rows does not eat them", () => {
  // 合并 miss 时持久化的 user 行会被追加到缓存末尾（例如带 plan 前缀的发送，乐观行
  // 文案对不上）。位置上看本轮瞬时行排在它前面，但时间上它们更新 —— 不能当残留丢掉。
  const persistedReply = {
    seq: 196,
    role: "agent",
    content: "reply",
    mindfs_id: "turn-196",
    timestamp: "2026-11-22T06:33:11.365Z",
  };
  const liveThought = {
    seq: 0,
    role: "thought",
    content: "本轮思考",
    timestamp: "2026-11-22T06:54:52.900Z",
  };
  const userAsk = {
    seq: 197,
    role: "user",
    content: "/plan 需要我对比什么?",
    mindfs_id: "turn-197",
    timestamp: "2026-11-22T06:54:52.438Z",
  };
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [persistedReply, liveThought, userAsk],
      visibleExchanges: [persistedReply, userAsk],
    }),
    [liveThought],
  );
});

test("live rows after the newest user turn stay in the overlay", () => {
  const persistedReply = {
    seq: 196,
    role: "agent",
    content: "reply",
    mindfs_id: "turn-196",
    timestamp: "2026-11-22T06:33:11.365Z",
  };
  const userAsk = {
    seq: 197,
    role: "user",
    content: "需要我对比什么?",
    mindfs_id: "turn-197",
    timestamp: "2026-11-22T06:54:52.438Z",
  };
  const liveThought = {
    seq: 0,
    role: "thought",
    content: "主要看三件事",
    timestamp: "2026-11-22T06:54:52.900Z",
  };
  const liveTool = {
    seq: 0,
    role: "tool",
    content: "",
    timestamp: "2026-11-22T06:54:53.100Z",
    toolCall: { callId: "call-live" },
  };
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [persistedReply, userAsk, liveThought, liveTool],
      visibleExchanges: [persistedReply, userAsk],
    }),
    [liveThought, liveTool],
  );
  // 迟到送达的本轮事件：时间戳还是本轮的旧值，但位置在边界之后 —— 仍然是活尾巴。
  const lateThought = {
    seq: 0,
    role: "thought",
    content: "迟到送达",
    timestamp: "2026-11-22T06:50:00.000Z",
  };
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [persistedReply, userAsk, liveThought, lateThought],
      visibleExchanges: [persistedReply, userAsk],
    }),
    [liveThought, lateThought],
  );
});

test("rows without mindfs_id and tool cards stay in the overlay", () => {
  const visible = row(4, "agent", "same text", "turn-4");
  const noIdentity = row(0, "agent", "same text", "");
  const tool = { role: "tool", mindfs_id: "turn-4", toolCall: { callId: "call-4" } };
  assert.deepEqual(
    computeTailOverlay({
      exchanges: [visible, noIdentity, tool],
      visibleExchanges: [visible],
    }),
    [noIdentity, tool],
  );
});
