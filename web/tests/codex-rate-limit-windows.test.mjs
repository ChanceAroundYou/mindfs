import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { translateWithLocale } from "../src/i18n/index.tsx";
import { pickTightestLimit } from "../src/components/agent/useCodexRateLimit.ts";

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

test("两个限额合并成一个胶囊，胶囊跟随剩余更少的窗口", () => {
  assert.match(hook, /remaining: remainingPercent\(window\)/);
  assert.match(hook, /pickTightestLimit\(limits\)/);
  assert.match(indicator, /tightestLimit\.resetText/);
  assert.match(indicator, /t\("codexLimit\.limitValueWithReset", \{ name: tightestLimit\.label, percent: chipPercent, time: tightestLimit\.resetText \}\)/);
  // 胶囊只渲染一个（不再 limits.map 并排），完整数据留在详情弹层里。
  assert.doesNotMatch(indicator, /limits\.map\(\(limit\) => \{/);
  assert.doesNotMatch(indicator, /codexLimit\.weeklyTitle/);
});

test("pickTightestLimit 取剩余最少者，缺值时稳定退回", () => {
  const view = (id, remaining) => ({ id, label: id, remaining, resetText: "" });
  assert.equal(pickTightestLimit([view("five_hour", 79), view("weekly", 31)]).id, "weekly");
  assert.equal(pickTightestLimit([view("five_hour", 12), view("weekly", 31)]).id, "five_hour");
  // 并列保留窗口顺序，避免两次渲染换标签。
  assert.equal(pickTightestLimit([view("five_hour", 31), view("weekly", 31)]).id, "five_hour");
  // 首帧某一条还没数值时，用有数值的那条；都没有才退回第一条。
  assert.equal(pickTightestLimit([view("five_hour", null), view("weekly", 31)]).id, "weekly");
  assert.equal(pickTightestLimit([view("five_hour", null), view("weekly", null)]).id, "five_hour");
  assert.equal(pickTightestLimit([view("weekly", 8)]).id, "weekly");
  assert.equal(pickTightestLimit([]), null);
});

test("点击胶囊打开完整详情，里面同时列出两条窗口", () => {
  assert.match(indicator, /onClick=\{\(\) => setDetailsOpen\(true\)\}/);
  assert.match(indicator, /aria-haspopup="dialog"/);
  assert.match(indicator, /\{details\}/);
  assert.match(indicator, /limits\.map\(\(limit, index\)/);
  assert.match(indicator, /t\("codexLimit\.detailsRemaining", \{ percent \}\)/);
  assert.match(indicator, /t\("codexLimit\.detailsResetAt", \{ time: limit\.resetText \}\)/);
  assert.match(indicator, /t\("codexLimit\.detailsResetIn", \{ time: limit\.resetText \}\)/);
  assert.match(indicator, /t\("codexLimit\.detailsResetCredits", \{ count: resetCount \}\)/);
  for (const [src, name] of [
    [zh, "codexLimit.badgeTitle"],
    [en, "codexLimit.badgeTitle"],
    [zh, "codexLimit.detailsTitle"],
    [en, "codexLimit.detailsTitle"],
    [zh, "codexLimit.detailsDescription"],
    [en, "codexLimit.detailsDescription"],
    [zh, "codexLimit.detailsRemaining"],
    [en, "codexLimit.detailsRemaining"],
    [zh, "codexLimit.detailsResetAt"],
    [en, "codexLimit.detailsResetAt"],
    [zh, "codexLimit.detailsResetIn"],
    [en, "codexLimit.detailsResetIn"],
    [zh, "codexLimit.detailsResetCredits"],
    [en, "codexLimit.detailsResetCredits"],
    [zh, "codexLimit.detailsEmpty"],
    [en, "codexLimit.detailsEmpty"],
  ]) {
    assert.ok(src.includes(`"${name}":`), `missing ${name}`);
  }
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
