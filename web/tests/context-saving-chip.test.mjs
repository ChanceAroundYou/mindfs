import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const routes = read("../../server/internal/api/http_routes.go");
const ledgerGo = read("../../server/internal/agent/fold/ledger.go");
const service = read("../src/services/agent/contextSaving.ts");
const hook = read("../src/components/agent/useContextSaving.ts");
const indicator = read("../src/components/agent/ContextSavingIndicator.tsx");
const chips = read("../src/components/action/ComposerTopChips.tsx");
const zh = read("../src/i18n/locales/zh-CN-agents.ts");
const en = read("../src/i18n/locales/en-US-agents.ts");

const CONTEXT_SAVING_KEYS = [
  "badge",
  "badgeTitle",
  "title",
  "description",
  "badgeRequest",
  "badgeSaved",
  "badgeSavedTotal",
  "badgeBlocks",
  "badgeTokens",
  "badgeRewrite",
  "badgeCacheHit",
  "requestTokensValue",
  "savedValue",
  "savedTotalValue",
  "blocksValue",
  "blocksSplitValue",
  "blocksSplitTokensValue",
  "cacheHitValue",
  "tokensValue",
  "unknown",
  "empty",
  "estimateNote",
  "rewriteTotal",
  "usageMissing",
];

test("前后端用同一条路径：会话 context-saving", () => {
  // 路径拼错时 chip 永远不出现，前端不报错、后端只会 404 —— 只能靠对齐钉住。
  assert.match(routes, /r\.Get\("\/api\/sessions\/\{key\}\/context-saving", h\.handleSessionContextSaving\)/);
  assert.match(service, /appURL\(`\/api\/sessions\/\$\{encodeURIComponent\(sessionKey\)\}\/context-saving`, params, nodeId\)/);
});

test("数据面必须带节点与 root，不能静默打激活节点", () => {
  assert.match(service, /if \(rootId\) params\.set\("root", rootId\)/);
  assert.match(service, /, params, nodeId\)/);
  assert.match(chips, /<ContextSavingIndicator[\s\S]*?rootId=\{currentRootId \|\| undefined\}[\s\S]*?nodeId=\{rootNodeId\}/);
});

test("只在有节省或折叠过时才显示胶囊", () => {
  assert.match(hook, /const savedBytes = contextSavingBytes\(saving\);/);
  assert.match(hook, /const visible = savedBytes > 0 \|\| !!saving\?\.requests;/);
  assert.match(indicator, /if \(!visible\) return null;/);
});

test("胶囊只挂在已绑定会话上，命令模式不显示", () => {
  assert.match(
    chips,
    /\{hasBoundSession && mode !== "command" \? \(\s*<ContextSavingIndicator/,
  );
  assert.match(chips, /const currentSessionKey = String\(boundSession\?\.key \|\| boundSession\?\.session_key \|\| ""\);/);
});

test("回合结束才刷新一次，不按秒轮询", () => {
  // 一次折叠只发生在回合结束时，轮询既浪费又看不出变化。
  assert.match(hook, /if \(wasPending && !pending\) void refresh\(\);/);
  assert.doesNotMatch(hook, /setInterval/);
});

test("会话切换时先清空旧数据，避免上一条会话的数字留在新会话上", () => {
  assert.match(hook, /setSaving\(null\);/);
  assert.match(hook, /if \(request === requestRef\.current\) setSaving\(next\);/);
});

test("详情面板先给结论：本轮请求从多大变成多大、省了百分之几", () => {
  // 两行分开写「原请求大小 / 本轮实际发送」时，读者要自己做减法；
  // 一行箭头加百分比，结论直接可读。两端都是同一份 body 的 token 估算。
  assert.match(
    indicator,
    /t\("contextSaving\.requestTokensValue", \{\s*req: formatTokensEstimate\(latest\.req_tokens_est\),\s*forwarded: formatTokensEstimate\(latest\.forward_tokens_est\),?\s*\}\)/,
  );
  assert.match(
    indicator,
    /formatPercent\(tokenSized \? savedTokens : latest\.saved_bytes, tokenSized \? latest\.req_tokens_est : latest\.req_bytes\)/,
  );
  assert.match(indicator, /t\("contextSaving\.savedValue", \{ size: savedText, percent: savedPercent \}\)/);
  assert.match(
    indicator,
    /latest\.new_blocks > 0\s*\?\s*latest\.new_tokens_est > 0\s*\?\s*t\("contextSaving\.blocksSplitTokensValue", \{ new: String\(latest\.new_blocks\), tokens: formatTokensEstimate\(latest\.new_tokens_est\), total: String\(latest\.blocks\) \}\)\s*:\s*t\("contextSaving\.blocksSplitValue", \{ new: String\(latest\.new_blocks\), total: String\(latest\.blocks\) \}\)\s*:\s*t\("contextSaving\.blocksValue", \{ count: String\(latest\.blocks\) \}\)/,
  );
});

test("单位统一成 token：请求、节省、折叠内容、重算都用同一种数法", () => {
  // 2026-11-21 之前一屏里 KB 与 token 混排，「省了 27 KB」和「折了 10,747 tokens」
  // 之间没有可比的换算。现在全部 token；旧账本行缺估算值时整块退回字节。
  assert.match(indicator, /const tokenSized = !!latest && latest\.req_tokens_est > 0 && latest\.forward_tokens_est > 0;/);
  assert.match(indicator, /function formatTokens\(/);
  assert.match(indicator, /\.replace\(\/\\B\(\?=\(\\d\{3\}\)\+\(\?!\\d\)\)\/g, ","\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokensEstimate\(latest\.tokens_est\) \}\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokens\(latest\.rewrite_tokens\) \}\)/);
  assert.match(indicator, /rewrite: formatTokens\(saving\.total_rewrite_tokens\)/);
  // 胶囊只说数：单位在 tooltip 与明细里，9px 的芯片能省一个词就省。
  assert.match(indicator, /const savedLabel = tokenSized\s*\?\s*formatTokensCompact\(savedTokens\)\s*:\s*formatBytes\(savedBytes\)/);
  assert.match(
    indicator,
    /const savedTitle = tokenSized\s*\?\s*t\("contextSaving\.tokensValue", \{ count: formatTokens\(savedTokens\) \}\)\s*:\s*formatBytes\(savedBytes\)/,
  );
  assert.match(indicator, /t\("contextSaving\.badge", \{ saved: savedLabel \}\)/);
  // 折叠耗时对读者没有下一步动作可做，不再占一行。
  assert.doesNotMatch(indicator, /fold_ms|badgeFoldMs/);
  // 关闭按钮不要写成「取消」——这里没有可取消的操作。
  assert.match(indicator, /t\("common\.close"\)/);
});

test("旧账本行没有 token 估算：退回字节，不拿字节除 4 反推", () => {
  // 反推在 CJK 内容上会差近一倍；宁可这一条继续显示 KB，也不编一个 token 数。
  assert.match(indicator, /`\$\{formatBytes\(latest\.req_bytes\)\} → \$\{formatBytes\(latest\.folded_bytes\)\}`/);
  assert.match(indicator, /: formatBytes\(latest\.saved_bytes\)/);
});

test("估算值只给三位有效数字，真实用量保持精确", () => {
  // token 估算是 ±30% 的量级，118,460 是假精度；重算 783 是上游真实用量，精确到个位。
  assert.match(indicator, /function formatTokensEstimate\(/);
  assert.match(indicator, /req: formatTokensEstimate\(latest\.req_tokens_est\)/);
  assert.match(indicator, /forwarded: formatTokensEstimate\(latest\.forward_tokens_est\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokensEstimate\(latest\.tokens_est\) \}\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokens\(latest\.rewrite_tokens\) \}\)/);
});

test("缓存命中行：折叠的代价是前缀缓存失效，命中率必须看得到", () => {
  // 只报「重算 783」看不出折叠值不值；cache_read 占比才是那一轮的缓存代价。
  assert.match(
    indicator,
    /const cacheTotal = \(latest\?\.usage_input \|\| 0\) \+ \(latest\?\.usage_cache_creation \|\| 0\) \+ \(latest\?\.usage_cache_read \|\| 0\);/,
  );
  assert.match(
    indicator,
    /const cachePercent = latest && cacheTotal > 0 \? Math\.round\(\(latest\.usage_cache_read \* 100\) \/ cacheTotal\) : null;/,
  );
  assert.match(
    indicator,
    /t\("contextSaving\.cacheHitValue", \{ percent: `\$\{cachePercent\}%`, tokens: formatTokens\(latest\.usage_cache_read\) \}\)/,
  );
  // 没读到用量时不能报 0% 命中。
  assert.match(indicator, /t\("contextSaving\.badgeCacheHit"\),\s*cachePercent === null\s*\?\s*t\("contextSaving\.unknown"\)/);
});

test("胶囊接口只回汇总，不下发 200 行历史", () => {
  // 前端类型里有 recent 但从未读取；每次点开胶囊传 40–60 KB 没有意义。
  assert.doesNotMatch(service, /recent/);
  // gofmt 会把字段名补齐成 `Recent       []LedgerEntry`，正则必须容忍对齐空白。
  assert.doesNotMatch(ledgerGo, /Recent\s+\[\]LedgerEntry/);
});

test("不再显示「已折叠 N 次请求」那一行", () => {
  // 这一行说的是历史，不是这一轮；面板只回答本轮省了多少。
  assert.doesNotMatch(indicator, /contextSaving\.history/);
  assert.doesNotMatch(zh, /contextSaving\.history/);
  assert.doesNotMatch(en, /contextSaving\.history/);
});

test("折叠的代价与收益并列显示：本轮重算 token 与节省同屏", () => {
  // 只报节省会让人以为折叠免费；rewrite_tokens 是那一请求按 1x 计价的部分。
  assert.match(service, /rewrite_tokens: number;/);
  assert.match(service, /total_rewrite_tokens: number;/);
  // 2026-11-20 起「重算」含缓存写入，缺失样本用 usage_source 标明，不再靠负号猜。
  assert.match(service, /rewrite_tokens: number;/);
  assert.match(service, /usage_cache_creation: number;/);
  assert.match(service, /usage_source: "response" \| "missing" \| ""/);
  assert.match(indicator, /latest\.usage_source === "missing"[\s\S]{0,80}t\("contextSaving\.unknown"\)/);
  assert.match(indicator, /saving\.usage_missing > 0/);
  assert.match(indicator, /t\("contextSaving\.rewriteTotal", \{ rewrite: formatTokens\(saving\.total_rewrite_tokens\) \}\)/);
  // -1 是「这一轮没读到用量」，不能显示成 0 或省略。
  assert.match(indicator, /saving\.total_rewrite_tokens > 0/);
});

test("累计节省覆盖整个保留窗口，一次折叠时不多写一行", () => {
  // 一轮折 5% 不起眼，整个会话累计起来才是用户在付的账。窗口就是汇总里的
  // requests（最近 200 行）；只有一条记录时合计等于本轮，重复一行没有信息。
  assert.match(indicator, /if \(saving && saving\.requests > 1\) \{/);
  assert.match(
    indicator,
    /const totalText = saving\.saved_token_samples > 0\s*\?\s*t\("contextSaving\.tokensValue", \{ count: formatTokens\(saving\.total_saved_tokens\) \}\)\s*:\s*formatBytes\(saving\.total_saved_bytes\);/,
  );
  assert.match(indicator, /t\("contextSaving\.savedTotalValue", \{ size: totalText, count: String\(saving\.requests\) \}\)/);
  assert.match(indicator, /saved_token_samples > 0/);
});

test("本轮新折报的是原文大小，不只是块数", () => {
  // 「新折 2 块」可以是一千 token，也可以是十万；同一行必须给出大小。
  assert.match(
    indicator,
    /latest\.new_tokens_est > 0\s*\?\s*t\("contextSaving\.blocksSplitTokensValue", \{ new: String\(latest\.new_blocks\), tokens: formatTokensEstimate\(latest\.new_tokens_est\), total: String\(latest\.blocks\) \}\)/,
  );
});

test("中转报告的用量要标明是估算，直连上游的不能误标", () => {
  // 判据曾经是「有缓存读、没有缓存写」，它把每一次 Codex 回合一律标成中转
  // 估算——官方 API 的冷前缀本来就零缓存写入。现在由代理按转发主机记来源，
  // 前端只读 usage_provenance，不许再从计数反推。
  assert.match(indicator, /const usageEstimated = !!latest && latest\.usage_provenance === "relay";/);
  assert.doesNotMatch(indicator, /latest\.usage_cache_creation === 0/);
  assert.match(indicator, /saving && usageEstimated[\s\S]{0,400}t\("contextSaving\.estimateNote"\)/);
  assert.match(service, /usage_provenance: "upstream" \| "relay" \| ""/);
  assert.match(service, /new_tokens_est: number;/);
  assert.match(service, /total_saved_bytes: number;/);
  assert.match(service, /total_saved_tokens: number;/);
  assert.match(service, /saved_token_samples: number;/);
});

test("文案在两种语言里成对存在", () => {
  for (const key of CONTEXT_SAVING_KEYS) {
    assert.ok(zh.includes(`"contextSaving.${key}":`), `zh-CN missing contextSaving.${key}`);
    assert.ok(en.includes(`"contextSaving.${key}":`), `en-US missing contextSaving.${key}`);
  }
  // 占位符对不上会让徽标显示成字面量 {saved}。
  assert.match(zh, /"contextSaving\.badge": "上下文 -\{saved\}"/);
  assert.match(en, /"contextSaving\.badge": "Context -\{saved\}"/);
  assert.match(zh, /"contextSaving\.requestTokensValue": "\{req\} → \{forwarded\} tokens"/);
  assert.match(en, /"contextSaving\.requestTokensValue": "\{req\} → \{forwarded\} tokens"/);
  // 「折叠内容 10,747」与「本轮节省 6,630」并列像自相矛盾；前者是原文大小。
  assert.match(zh, /"contextSaving\.badgeTokens": "折叠原文"/);
  assert.match(en, /"contextSaving\.badgeTokens": "Folded source"/);
  assert.match(zh, /"contextSaving\.blocksSplitValue": "本轮新折 \{new\} 块 · 共 \{total\} 块"/);
  assert.match(zh, /"contextSaving\.blocksSplitTokensValue": "本轮新折 \{new\} 块（\{tokens\} tokens）· 共 \{total\} 块"/);
  assert.match(zh, /"contextSaving\.savedTotalValue": "\{size\}（最近 \{count\} 次折叠）"/);
  assert.match(zh, /"contextSaving\.cacheHitValue": "\{percent\}（\{tokens\} tokens）"/);
});
