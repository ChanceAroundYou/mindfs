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
