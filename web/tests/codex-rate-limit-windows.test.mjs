import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const backend = read("../../server/internal/agent/codex/rate_limits.go");
const service = read("../src/services/agent/codexRateLimits.ts");
const hook = read("../src/components/agent/useCodexRateLimit.ts");
const indicator = read("../src/components/agent/CodexRateLimitIndicator.tsx");
const zh = read("../src/i18n/locales/zh-CN-agents.ts");
const en = read("../src/i18n/locales/en-US-agents.ts");

test("后端保留 5 小时限额窗口，且不把 5 小时窗当周限", () => {
  assert.match(backend, /FiveHour\s+\*RateLimitWindow\s+`json:"five_hour,omitempty"`/);
  assert.match(backend, /func selectFiveHourWindow\(snapshot rawRateLimitSnapshot\) \*RateLimitWindow/);
  assert.match(backend, /FiveHour: selectFiveHourWindow\(snapshot\)/);
  assert.match(backend, /!isWindowDuration\(snapshot\.Secondary, fiveHourWindowMinutes\)/);
});

test("前端类型与限额视图都包含 5 小时窗口", () => {
  assert.match(service, /five_hour\?: CodexRateLimitWindow;/);
  assert.match(hook, /push\("five_hour", t\("codexLimit\.fiveHour"\), status\?\.five_hour\)/);
  assert.match(hook, /push\("weekly", t\("codexLimit\.weekly"\), status\?\.weekly\)/);
});

test("每个限额合并展示剩余与重置时间", () => {
  assert.match(hook, /remaining: remainingPercent\(window\)/);
  assert.match(hook, /resetTime: formatResetTime\(window\.resets_at\)/);
  assert.match(indicator, /t\("codexLimit\.limitValueWithReset", \{ name: limit\.label, percent, time: limit\.resetTime \}\)/);
  assert.match(indicator, /limits\.map\(\(limit\)/);
  assert.doesNotMatch(indicator, /codexLimit\.weeklyTitle/);
});

test("限额文案在两种语言里成对存在", () => {
  for (const [src, name, value] of [
    [zh, "codexLimit.fiveHour", "5小时"],
    [en, "codexLimit.fiveHour", "5h"],
  ]) {
    assert.match(src, new RegExp(`"${name.replace(".", "\\.")}": "${value}"`));
  }
  for (const key of ["codexLimit.limitValue", "codexLimit.limitValueWithReset", "codexLimit.limitTitle"]) {
    assert.ok(zh.includes(`"${key}":`), `zh-CN missing ${key}`);
    assert.ok(en.includes(`"${key}":`), `en-US missing ${key}`);
  }
  assert.doesNotMatch(zh, /"codexLimit\.weeklyTitle/);
  assert.doesNotMatch(en, /"codexLimit\.weeklyTitle/);
});
