import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { formatTokenAmount } from "../src/components/agent/tokenAmount";

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
  "badgeRequest",
  "badgeSaved",
  "badgeSavedTotal",
  "badgeBlocks",
  "badgeTokens",
  "badgeRewrite",
  "badgeRewriteTotal",
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
  "badgeContext",
  "hygieneActive",
  "hygieneDuration",
  "hygieneContext",
  "hygieneLastUsage",
  "hygieneLocalBudget",
  "hygieneLocalBudgetValue",
  "hygieneAutoIn",
  "hygieneAutoInValue",
  "hygieneHintTitle",
  "hygieneNoticeTitle",
  "hygieneHintNotice",
  "hygieneNoticeNotice",
  "hygieneAutoFailed",
  "compactNow",
  "compacting",
  "compactFailedDetail",
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

test("只在有节省、折叠过、或超长会话需要提示时才显示胶囊", () => {
  assert.match(hook, /const savedBytes = contextSavingBytes\(saving\);/);
  assert.match(hook, /const hygiene = saving\?\.hygiene \?\? null;/);
  assert.match(hook, /const hygieneBand = hygiene\?\.state === "notice" \|\| hygiene\?\.state === "hint";/);
  assert.match(hook, /const visible = savedBytes > 0 \|\| !!saving\?\.requests \|\| hygieneBand;/);
  assert.match(indicator, /if \(!visible\) return null;/);
});

test("胶囊只挂在已绑定会话上，命令模式不显示", () => {
  assert.match(
    chips,
    /\{hasBoundSession && mode !== "command" \? \(\s*<ContextSavingIndicator/,
  );
  assert.match(chips, /const currentSessionKey = String\(boundSession\?\.key \|\| boundSession\?\.session_key \|\| ""\);/);
});

test("普通回合结束才刷新一次；只有压缩中才按秒轮询", () => {
  // 一次折叠只发生在回合结束时，普通回合轮询既浪费又看不出变化。压缩回合
  // 真实耗时以分钟计，服务端 pending 为真时才需要补拉。
  assert.match(hook, /if \(!wasPending \|\| pending\) return;/);
  assert.match(hook, /void refresh\(\);[\s\S]*?settleTimerRef\.current = window\.setTimeout\(\(\) => \{/);
  assert.match(hook, /\}, 2000\);/);
  assert.match(hook, /if \(!hygiene\?\.compact_pending\) \{[\s\S]*?return;\s*\}/);
  assert.match(hook, /compactPollRef\.current = true;\s*const timer = window\.setInterval/);
});

test("延迟刷新与压缩兜底各用独立定时器", () => {
  // 压缩回合结束也会走 pending 回落；共用定时器会让按钮永远停在「压缩中」。
  assert.match(hook, /const compactFallbackRef = useRef<number \| null>\(null\);/);
  assert.match(hook, /const settleTimerRef = useRef<number \| null>\(null\);/);
  const compactBlock = hook.slice(hook.indexOf("const compact = async"));
  assert.match(compactBlock, /compactFallbackRef\.current = window\.setTimeout/);
  assert.doesNotMatch(compactBlock, /settleTimerRef/);
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
    /t\("contextSaving\.requestTokensValue", \{\s*req: formatTokenAmount\(latest\.req_tokens_est\),\s*forwarded: formatTokenAmount\(latest\.forward_tokens_est\),?\s*\}\)/,
  );
  assert.match(
    indicator,
    /const billingSized = !!latest && latest\.usage_source !== "missing" && billingContext > 0 && foldedTokens > 0;/,
  );
  assert.match(
    indicator,
    /formatPercent\(\s*billingSized \? foldedTokens : tokenSized \? savedTokens : latest\.saved_bytes,\s*billingSized \? billingContext \+ foldedTokens : tokenSized \? latest\.req_tokens_est : latest\.req_bytes,\s*\)/,
  );
  assert.match(indicator, /t\("contextSaving\.savedValue", \{ size: savedText, percent: savedPercent \}\)/);
  assert.match(
    indicator,
    /latest\.new_blocks > 0\s*\?\s*latest\.new_tokens_est > 0\s*\?\s*t\("contextSaving\.blocksSplitTokensValue", \{ new: String\(latest\.new_blocks\), tokens: formatTokenAmount\(latest\.new_tokens_est\), total: String\(latest\.blocks\) \}\)\s*:\s*t\("contextSaving\.blocksSplitValue", \{ new: String\(latest\.new_blocks\), total: String\(latest\.blocks\) \}\)\s*:\s*t\("contextSaving\.blocksValue", \{ count: String\(latest\.blocks\) \}\)/,
  );
});

test("单位统一成 token：请求、节省、折叠内容、重算都用同一种数法", () => {
  // 2026-11-21 之前一屏里 KB 与 token 混排，「省了 27 KB」和「折了 10,747 tokens」
  // 之间没有可比的换算。现在全部 token；旧账本行缺估算值时整块退回字节。
  assert.match(indicator, /const tokenSized = !!latest && latest\.req_tokens_est > 0 && latest\.forward_tokens_est > 0;/);
  assert.doesNotMatch(indicator, /function formatTokens\(|function formatTokensEstimate\(|function formatTokensCompact\(/);
  assert.match(indicator, /import \{ formatTokenAmount \} from "\.\/tokenAmount";/);
  assert.match(read("../src/components/agent/tokenAmount.ts"), /\.replace\(\/\\B\(\?=\(\\d\{3\}\)\+\(\?!\\d\)\)\/g, ","\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(latest\.tokens_est\) \}\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(latest\.rewrite_tokens\) \}\)/);
  assert.match(indicator, /count: formatTokenAmount\(saving\.total_rewrite_tokens\)/);
  // 胶囊只说数：单位在 tooltip 与明细里，9px 的芯片能省一个词就省。
  assert.match(indicator, /const savedLabel = tokenSized\s*\?\s*formatTokenAmount\(savedTokens\)\s*:\s*formatBytes\(savedBytes\)/);
  assert.match(
    indicator,
    /const savedTitle = tokenSized\s*\?\s*t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(savedTokens\) \}\)\s*:\s*formatBytes\(savedBytes\)/,
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
  assert.match(indicator, /req: formatTokenAmount\(latest\.req_tokens_est\)/);
  assert.match(indicator, /forwarded: formatTokenAmount\(latest\.forward_tokens_est\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(latest\.tokens_est\) \}\)/);
  assert.match(indicator, /t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(latest\.rewrite_tokens\) \}\)/);
});

test("缓存命中行：折叠的代价是前缀缓存失效，命中率必须看得到", () => {
  // 只报「重算 783」看不出折叠值不值；cache_read 占比才是那一轮的缓存代价。
  assert.match(indicator, /const cacheTotal = billingContext;/);
  assert.match(
    indicator,
    /const cachePercent = latest && cacheTotal > 0 \? Math\.round\(\(latest\.usage_cache_read \* 100\) \/ cacheTotal\) : null;/,
  );
  assert.match(
    indicator,
    /t\("contextSaving\.cacheHitValue", \{ percent: `\$\{cachePercent\}%`, tokens: formatTokenAmount\(latest\.usage_cache_read\) \}\)/,
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
  // 缺失样本数只进账本，不再单独占一行提示；命中率那行仍标「未知」。
  // 2026-11-21 起合计是普通数据行，不再是脚注句子。
  assert.match(
    indicator,
    /t\("contextSaving\.badgeRewriteTotal"\),\s*t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(saving\.total_rewrite_tokens\) \}\)/,
  );
  // -1 是「这一轮没读到用量」，不能显示成 0 或省略。
  assert.match(indicator, /saving\.total_rewrite_tokens > 0/);
});

test("累计节省只显示总量，不解释估算也不报窗口长度", () => {
  // 一轮折 5% 不起眼，整个会话累计起来才是用户在付的账；200 行窗口与
  // token 估算属性都是口径说明，不是用户要的数字，不进胶囊。
  assert.match(indicator, /if \(saving && saving\.context_token_samples > 0 && saving\.total_folded_tokens > 0\) \{/);
  assert.match(
    indicator,
    /const totalPercent = formatPercent\(saving\.total_folded_tokens, saving\.total_context_tokens \+ saving\.total_folded_tokens\);/,
  );
  assert.match(indicator, /t\("contextSaving\.savedValue", \{ size: totalText, percent: totalPercent \}\)/);
  assert.match(indicator, /if \(saving && saving\.requests > 1\) \{/);
  assert.match(
    indicator,
    /const totalText = saving\.saved_token_samples > 0\s*\?\s*t\("contextSaving\.tokensValue", \{ count: formatTokenAmount\(saving\.total_saved_tokens\) \}\)\s*:\s*formatBytes\(saving\.total_saved_bytes\);/,
  );
  assert.match(indicator, /t\("contextSaving\.savedTotalValue", \{ size: totalText \}\)/);
  assert.doesNotMatch(indicator, /savedTotalValue[^\n]*count/);
  assert.match(indicator, /saved_token_samples > 0/);
  // 计费口径的累计来自账本字段，不是前端拿字节除出来的。
  assert.match(service, /total_context_tokens: number;/);
  assert.match(service, /total_folded_tokens: number;/);
  assert.match(service, /context_token_samples: number;/);
});

test("本轮新折报的是原文大小，不只是块数", () => {
  // 「新折 2 块」可以是一千 token，也可以是十万；同一行必须给出大小。
  assert.match(
    indicator,
    /latest\.new_tokens_est > 0\s*\?\s*t\("contextSaving\.blocksSplitTokensValue", \{ new: String\(latest\.new_blocks\), tokens: formatTokenAmount\(latest\.new_tokens_est\), total: String\(latest\.blocks\) \}\)/,
  );
});

test("用量来源只记在账本里，胶囊不再挂说明", () => {
  // 来源由代理按转发主机写进账本（B16）：官方 API 的冷前缀本来就零缓存写入，
  // 从计数器反推必然把 Codex 回合误标成中转。2026-11-21 起胶囊不显示任何说明
  // 文字——标题、数据行、合计行之外不再有整段提示。
  assert.match(service, /usage_provenance: "upstream" \| "relay" \| ""/);
  assert.doesNotMatch(indicator, /usage_provenance|estimateNote|contextSaving\.description|contextSaving\.rewriteTotal/);
  assert.doesNotMatch(zh, /contextSaving\.(description|estimateNote|rewriteTotal)/);
  assert.doesNotMatch(en, /contextSaving\.(description|estimateNote|rewriteTotal)/);
  assert.match(service, /new_tokens_est: number;/);
  assert.match(service, /total_saved_bytes: number;/);
  assert.match(service, /total_saved_tokens: number;/);
  assert.match(service, /saved_token_samples: number;/);
});

test("胶囊带回超长会话状态，字段与服务端 HygieneView 同名", () => {
  assert.match(service, /hygiene: SessionHygiene;/);
  for (const key of [
    "state",
    "enabled",
    "active_seconds",
    "context_tokens",
    "context_window",
    "context_percent",
    "context_limit",
    "over_line_turns",
    "turns_over_line",
    "turns_until_auto_compact",
    "auto_compact_due",
    "auto_failed",
  ]) {
    assert.match(service, new RegExp(`${key}:`), `SessionHygiene missing ${key}`);
  }
});

test("距上次压缩的活跃时长不再按档位隐藏", () => {
  // 用户报过「累计活跃时间从胶囊详情里消失了」：原来这两行只在 notice/hint 出现，
  // 没到阈值的会话完全看不到。现在只要 hygiene 开着就渲染，只有倒计时仍只给 hint。
  assert.match(indicator, /if \(hygiene\?\.enabled\) \{/);
  assert.doesNotMatch(indicator, /if \(hygieneBand && hygiene\) \{/);
  assert.match(zh, /contextSaving\.hygieneActive": "距上次压缩"/);
  assert.match(en, /contextSaving\.hygieneActive": "Since last compact"/);
  assert.match(zh, /contextSaving\.hygieneContextValue": "\{tokens\} \/ \{limit\}"/);
});

test("又大不久只提示：不显示倒计时，也不提供自动触发", () => {
  // 倒计时只在 hint 档出现；notice 档只有「上下文多大、活跃多久」两行。
  assert.match(indicator, /if \(hygiene\.state === "hint" && turnsUntilAuto >= 0\) \{/);
  assert.match(indicator, /t\("contextSaving\.hygieneNoticeNotice"\)/);
  assert.match(indicator, /t\("contextSaving\.hygieneAutoInValue", \{ count: String\(turnsUntilAuto\) \}\)/);
});

test("又久又大：胶囊显示倒计时，并给一个「立即压缩」入口", () => {
  assert.match(indicator, /t\("contextSaving\.hygieneActive"\)/);
  assert.match(indicator, /t\("contextSaving\.hygieneContext"\)/);
  assert.match(indicator, /onClick=\{\(\) => void compact\(\)\}/);
  assert.match(indicator, /compactBusy \? t\("contextSaving\.compacting"\) : t\("contextSaving\.compactNow"\)/);
  assert.match(indicator, /disabled=\{compactBusy\}/);
  // 手动压缩走一条独立的 POST：它只要把回合排出去，结果通过读口的 pending 回传。
  assert.match(hook, /await compactSession\(sessionKey, rootId, nodeId\);/);
  assert.match(hook, /const compactBusy = compacting \|\| !!hygiene\?\.compact_pending;/);
  assert.doesNotMatch(hook, /\}, 8000\);/);
  assert.match(hook, /window\.setInterval\(\(\) => void refresh\(\), 2000\)/);
  assert.match(service, /compact_pending: boolean;/);
  assert.match(service, /compact_error: string;/);
  assert.match(service, /\/api\/sessions\/\$\{encodeURIComponent\(sessionKey\)\}\/compact/);
  assert.match(service, /\{ method: "POST" \}/);
  assert.match(routes, /r\.Post\("\/api\/sessions\/\{key\}\/compact", h\.handleSessionCompact\)/);
});

test("自动压缩失败后停手，并把失败原因写在胶囊里", () => {
  assert.match(indicator, /hygiene\?\.auto_failed/);
  assert.match(indicator, /t\("contextSaving\.hygieneAutoFailed"\)/);
  assert.match(indicator, /compact_error \? t\("contextSaving\.compactFailedDetail", \{ error: hygiene\.compact_error \}\)/);
});

test("胶囊带语言无关的稳定标识：端到端不靠中文文案认它", () => {
  // 无头 Chromium 的 navigator.language 是 en-US，aria-label 会变成英文；拿中文
  // 子串找胶囊的测试会一直等不到（2026-11-21 实测）。档位也要能从 DOM 上读。
  assert.match(indicator, /data-testid="context-saving-chip"/);
  assert.match(indicator, /data-hygiene=\{hygiene\?\.state \|\| "none"\}/);
});

test("没折叠过的会话也显示胶囊：它只报上下文规模，不报 -0", () => {
  assert.match(indicator, /const hasSaving = savedBytes > 0 \|\| !!saving\?\.requests;/);
  assert.match(indicator, /t\("contextSaving\.badgeContext", \{ tokens: hygieneTokens \}\)/);
  assert.match(indicator, /const chipLabel = hasSaving/);
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
  assert.match(zh, /"contextSaving\.savedTotalValue": "\{size\}"/);
  assert.match(en, /"contextSaving\.savedTotalValue": "\{size\}"/);
  assert.doesNotMatch(zh, /savedTotalValue[^\n]*200/);
  assert.match(zh, /"contextSaving\.cacheHitValue": "\{percent\}（\{tokens\} tokens）"/);
  assert.match(zh, /"contextSaving\.badgeRewriteTotal": "重算合计"/);
  assert.match(en, /"contextSaving\.badgeRewriteTotal": "Rewritten in total"/);
});

test("上次真实 usage 与本地预检线分开显示，预检线标为估算", () => {
  // 用户看到「上下文没满」却被 CLI 在发请求前拒绝：胶囊此前只报上一次成功请求的
  // usage。这里把真实 usage 与本地预检预算（窗口 - 20k 输出预留 - 3k 余量）并列，
  // 后者明确是估算，不冒充上游计量。
  assert.match(indicator, /const contextWindow = hygiene\?\.context_window \|\| 0;/);
  assert.match(indicator, /const localBlockLine = contextWindow > 0 \? contextWindow - 20000 - 3000 : 0;/);
  assert.match(indicator, /const localPrecheck = \(hygiene\?\.context_tokens \|\| 0\) \+ 20000 \+ 3000;/);
  assert.match(indicator, /t\("contextSaving\.hygieneLastUsage"\)/);
  assert.match(
    indicator,
    /t\("contextSaving\.hygieneLocalBudgetValue", \{\s*used: formatTokenAmount\(localPrecheck\),\s*limit: formatTokenAmount\(localBlockLine\),?\s*\}\)/,
  );
  assert.match(zh, /"contextSaving\.hygieneLastUsage":/);
  assert.match(en, /"contextSaving\.hygieneLastUsage":/);
  assert.match(zh, /"contextSaving\.hygieneLocalBudgetValue": "[^"]*估算/);
  assert.match(en, /"contextSaving\.hygieneLocalBudgetValue": "[^"]*estimate/);
});

test("没读到用量的样本只留在账本里，不再在胶囊里提示", () => {
  assert.doesNotMatch(indicator, /contextSaving\.usageMissing/);
  assert.doesNotMatch(zh, /"contextSaving\.usageMissing":/);
  assert.doesNotMatch(en, /"contextSaving\.usageMissing":/);
});
