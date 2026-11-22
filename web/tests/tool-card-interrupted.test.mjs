import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { test } from "vitest";

// 恢复后的工具卡状态是 interrupted：界面必须把它算作失败态，不能当成已完成。
test("interrupted tool calls render as failed", () => {
  const readSrc = (name) => readFileSync(new URL(`../src/components/stream/${name}`, import.meta.url), "utf8");
  const source = readSrc("ToolCallGroupCard.tsx");
  assert.match(
    source,
    /status === "interrupted"/,
    "ToolCallGroupCard 必须把 interrupted 归到失败态",
  );
  assert.match(
    readSrc("useToolCallCard.ts"),
    /normalizedStatus === "interrupted"/,
    "单卡也要把 interrupted 判为失败",
  );
  assert.match(
    readSrc("toolCallRender.tsx"),
    /interrupted: "#ef4444"/,
    "interrupted 应使用失败色",
  );
});
