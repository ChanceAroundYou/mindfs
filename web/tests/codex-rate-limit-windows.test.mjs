import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { translateWithLocale } from "../src/i18n/index.tsx";

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
  assert.match(indicator, /t\("codexLimit\.limitValueWithReset", \{ name: limit\.label, percent, time: limit\.resetText \}\)/);
  assert.match(indicator, /limits\.map\(\(limit\)/);
  assert.doesNotMatch(indicator, /codexLimit\.weeklyTitle/);
});

test("5 小时限额显示准确时刻，周限显示剩余天数与小时", () => {
  assert.match(hook, /if \(id === "five_hour"\)/);
  assert.match(hook, /formatClockTime\(window\.resets_at\)/);
  assert.match(hook, /formatRemaining\(window\.resets_at, nowMs\)/);
  assert.match(hook, /t\("codexLimit\.resetAtTime", \{ time: formatClockTime\(window\.resets_at\) \}\)/);
  assert.match(hook, /t\("codexLimit\.resetInRemaining", \{ days, hours \}\)/);
  // 剩余时间要跟着时钟走，不能是首帧算一次就冻住。
  assert.match(hook, /setInterval\(\(\) => setNowMs\(Date\.now\(\)\), 60_000\)/);
  for (const [src, name, value] of [
    [zh, "codexLimit.resetAtTime", "{time}"],
    [zh, "codexLimit.resetInRemaining", "{days}d {hours}h"],
  ]) {
    assert.ok(src.includes(`"${name}": "${value}"`), `zh-CN missing ${name}`);
  }
});

test("可用重置次数为 0 时不显示重置徽标", () => {
  assert.match(indicator, /loading \|\| resetCount > 0 \? \(/);
  assert.match(indicator, /\{t\("codexLimit\.reset"\)\}/);
});

test("限额文案不重复：模板里不再自带百分号与「重置」", () => {
  // 百分比由调用方带（"30%"），模板再补一个就成了 "30%%"；
  // 胶囊里不再出现「重置」二字，重置短语只由短语本身提供。
  assert.ok(zh.includes('"codexLimit.limitValue": "{name} {percent}"'));
  assert.ok(zh.includes('"codexLimit.limitValueWithReset": "{name} {percent} · {time}"'));
  assert.ok(zh.includes('"codexLimit.limitTitle": "{name} 剩余 {percent}，{time} 重置"'));
  assert.ok(en.includes('"codexLimit.limitValue": "{name} {percent}"'));
  assert.ok(en.includes('"codexLimit.limitValueWithReset": "{name} {percent} · {time}"'));
  assert.ok(en.includes('"codexLimit.limitTitle": "{name}: {percent} left, resets {time}"'));
  assert.match(indicator, /`\$\{limit\.remaining\}%`/);
});

test("拼出来的胶囊文案就是用户看到的那一串", () => {
  const t = (key, params) => translateWithLocale("zh-CN", key, params);
  const fiveHour = t("codexLimit.limitValueWithReset", {
    name: t("codexLimit.fiveHour"),
    percent: "79%",
    time: t("codexLimit.resetAtTime", { time: "16:56" }),
  });
  assert.equal(fiveHour, "5h 79% · 16:56");
  const weekly = t("codexLimit.limitValueWithReset", {
    name: t("codexLimit.weekly"),
    percent: "31%",
    time: t("codexLimit.resetInRemaining", { days: 4, hours: 23 }),
  });
  assert.equal(weekly, "周 31% · 4d 23h");
  assert.doesNotMatch(weekly, /%%/);
  assert.doesNotMatch(`${fiveHour}${weekly}`, /重置/, "胶囊里不出现「重置」二字");
});

test("限额文案在两种语言里成对存在", () => {
  for (const [src, name, value] of [
    [zh, "codexLimit.fiveHour", "5h"],
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
  assert.doesNotMatch(zh, /"codexLimit\.resetInRemainingShort"/);
  assert.doesNotMatch(en, /"codexLimit\.resetInRemainingShort"/);
});
