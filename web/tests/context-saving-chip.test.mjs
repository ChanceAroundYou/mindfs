import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const routes = read("../../server/internal/api/http_routes.go");
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
  "badgeBlocks",
  "badgeTokens",
  "badgeRewrite",
  "requestTokensValue",
  "savedValue",
  "blocksValue",
  "tokensValue",
  "unknown",
  "empty",
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
    /t\("contextSaving\.requestTokensValue", \{\s*req: formatTokens\(latest\.req_tokens_est\),\s*forwarded: formatTokens\(latest\.forward_tokens_est\),?\s*\}\)/,
  );
  assert.match(
    indicator,
    /formatPercent\(tokenSized \? savedTokens : latest\.saved_bytes, tokenSized \? latest\.req_tokens_est : latest\.req_bytes\)/,
  );
  assert.match(indicator, /t\("contextSaving\.savedValue", \{ size: savedText, percent: savedPercent \}\)/);
  assert.match(indicator, /t\("contextSaving\.badgeBlocks"\), t\("contextSaving\.blocksValue", \{ count: String\(latest\.blocks\) \}\)/);
});

test("单位统一成 token：请求、节省、折叠内容、重算都用同一种数法", () => {
  // 2026-11-21 之前一屏里 KB 与 token 混排，「省了 27 KB」和「折了 10,747 tokens」
  // 之间没有可比的换算。现在全部 token；旧账本行缺估算值时整块退回字节。
  assert.match(indicator, /const tokenSized = !!latest && latest\.req_tokens_est > 0 && latest\.forward_tokens_est > 0;/);
  assert.match(indicator, /function formatTokens\(/);
  assert.match(indicator, /\.replace\(\/\\B\(\?=\(\\d\{3\}\)\+\(\?!\\d\)\)\/g, ","\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokens\(latest\.tokens_est\) \}\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokens\(latest\.rewrite_tokens\) \}\)/);
  assert.match(indicator, /rewrite: formatTokens\(saving\.total_rewrite_tokens\)/);
  // 胶囊本身也按 token，且用紧凑写法；单位跟着数值走，旧账本行仍是 KB。
  assert.match(
    indicator,
    /t\("contextSaving\.tokensValue", \{ count: formatTokensCompact\(savedTokens\) \}\)\s*:\s*formatBytes\(savedBytes\)/,
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
});
