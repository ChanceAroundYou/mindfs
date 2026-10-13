import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
const root = new URL("../src/hooks/", import.meta.url);
const hook = readFileSync(new URL("useSessionStream.ts", root), "utf8");
const timeline = readFileSync(new URL("sessionTimeline.ts", root), "utf8");
const lines = (source) => source.trimEnd().split("\n").length;

assert.ok(lines(hook) < 600, `hook remains below the frontend hard line (${lines(hook)})`);
assert.ok(lines(timeline) < 600, `timeline helper remains below the frontend hard line (${lines(timeline)})`);
assert.match(hook, /export function useSessionStream\(/);
assert.match(hook, /export \{ groupConsecutiveToolCalls \} from "\.\/sessionTimeline"/);
assert.match(hook, /export type \{ TimelineItem \} from "\.\/sessionTimeline"/);
assert.match(timeline, /export function buildBaseTimeline\(/);
assert.match(timeline, /export function groupConsecutiveToolCalls\(/);
assert.match(timeline, /export function applySessionContextWindow\(/);
assert.doesNotMatch(timeline, /from "react"/);
console.log("session-stream-timeline-split.test.mjs: OK");
