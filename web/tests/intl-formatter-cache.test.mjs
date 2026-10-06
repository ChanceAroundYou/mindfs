import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * Intl 格式化器缓存的契约守卫（`Scope: G-AK`）。
 *
 * 为什么要单独钉：`new Intl.DateTimeFormat()` 的**构造**是重活，实测一次渲染 1000 条
 * 消息时逐条新建要 235ms，复用同一实例只要 14ms（16×）。SessionViewer 的逐条渲染
 * 会逐条调 formatTime —— 这层缓存一旦被「顺手简化」掉，性能是**静默**退化的，
 * 测试全绿、肉眼也看不出来，只有长会话卡一下才有人发现。
 *
 * 这一层不承担正确性（格式化结果与 Intl 本身一致），所以钉的是形状而不是输出。
 */

const root = path.resolve(import.meta.dirname, "..");
const source = fs.readFileSync(path.join(root, "src/i18n/index.tsx"), "utf8");

// 1) 四个格式化器都必须走缓存，不能在调用点直接 `new Intl.*Format`。
//    格式化器在 context 里按调用执行，所以在调用点新建等于每次渲染都新建。
assert.match(
  source,
  /formatDate: \(value, options\) => memoDateTimeFormat\(locale, options\)\.format\(/,
  "formatDate must go through the memoized formatter",
);
assert.match(
  source,
  /formatTime: \(value, options\) => memoDateTimeFormat\(locale, \{/,
  "formatTime must go through the memoized formatter",
);
assert.match(
  source,
  /formatDateTime: \(value, options\) => memoDateTimeFormat\(locale, \{/,
  "formatDateTime must go through the memoized formatter",
);
assert.match(
  source,
  /formatNumber: \(value, options\) => memoNumberFormat\(locale, options\)\.format\(/,
  "formatNumber must go through the memoized formatter",
);
assert.doesNotMatch(
  source,
  /format(Date|Time|DateTime|Number): \(value[^)]*\) => new Intl\./,
  "no format* may construct an Intl formatter at the call site",
);

// 2) 缓存必须有上限：无上限的 Map 在 options 动态变化时会一直涨。
//    （同一仓库里 file.ts 的缓存也是有界的，这里不能漏。）
assert.match(
  source,
  /const intlFormatterCacheMax = 64;/,
  "the formatter cache needs an explicit bound",
);
assert.match(
  source,
  /function evictOldest<T>\(cache: Map<string, T>\): void/,
  "the cache must evict its oldest entry instead of growing forever",
);

// 3) 缓存键必须与 option 键序无关 —— 调用方传 {hour} 与 {hour, hourCycle} 不能开出两条缓存。
assert.match(
  source,
  /Object\.keys\(record\)\s*\.sort\(\)/,
  "the cache key must not depend on the caller's option key order",
);
assert.match(
  source,
  /const key = `\$\{locale\}\\u0000\$\{stableOptionsKey\(options\)\}`;/,
  "locale and options must be separated in the cache key",
);

console.log("intl-formatter-cache.test.mjs: OK");
