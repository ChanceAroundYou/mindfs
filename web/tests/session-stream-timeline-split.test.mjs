import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

test("session-stream-timeline-split", async () => {
const root = new URL("../src/hooks/", import.meta.url);
const hook = readFileSync(new URL("useSessionStream.ts", root), "utf8");
const timeline = readFileSync(new URL("sessionTimeline.ts", root), "utf8");
const assistantTimeline = readFileSync(new URL("sessionAssistantTimeline.ts", root), "utf8");
const lines = (source) => source.trimEnd().split("\n").length;

assert.ok(lines(hook) <= 500, `hook stays within the frontend hard line (${lines(hook)})`);
assert.ok(lines(timeline) <= 500, `timeline helper stays within the frontend hard line (${lines(timeline)})`);
assert.ok(lines(assistantTimeline) <= 500, `assistant timeline helper stays within the frontend hard line (${lines(assistantTimeline)})`);
assert.match(hook, /export function useSessionStream\(/);
assert.match(hook, /export \{ groupConsecutiveToolCalls \} from "\.\/sessionTimeline"/);
assert.match(hook, /export type \{ TimelineItem \} from "\.\/sessionTimeline"/);
assert.match(timeline, /export function buildBaseTimeline\(/);
assert.match(timeline, /export function groupConsecutiveToolCalls\(/);
// 会话级 context_window 不再补丁进最后一个 turnMeta（那会拆散轮次身份、画出两条控件行）；
// 状态条直接从 exchange/会话取。
assert.doesNotMatch(timeline, /applySessionContextWindow/);
assert.match(timeline, /buildAssistantTimeline\(/);
assert.match(assistantTimeline, /export function buildAssistantTimeline\(/);
assert.doesNotMatch(timeline, /from "react"/);
assert.doesNotMatch(assistantTimeline, /from "react"/);
console.log("session-stream-timeline-split.test.mjs: OK");
});
