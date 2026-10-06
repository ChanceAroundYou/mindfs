import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 2026-10-06 — 「瞬时行不许按位置编造 seq」。
//
// 症状：会话切走再切回，ask 卡下面多出一整块正文，而且那块正文**自带工具卡**。
// 根因是 buildBaseTimeline 里的 inferredSeq —— 它给没有 seq 的行按「第几条 user/agent」
// 推一个位置，于是：
//   · 该行凭空拿到一个**已经属于某条真实持久行**的 seq；
//   · exchange_aux 是**按 seq 索引**的（工具卡 / token 用量挂在 aux[seq] 下），
//     这一行把别人的 aux 一并认领，用户看到的就是「正文下方多出一整套工具卡」；
//   · `data-session-seq` 与 fork 按钮都以 `seq > 0` 为「这是持久消息」的判据，
//     编造的 seq 让还没落盘的行看起来可以 fork。
//
// 排序本来就不需要它：瞬时行永远排在持久行之后（composeLoadedExchanges 的契约），
// 数组顺序即时间顺序。`seq` 是「服务端窗口里的身份」，不该由位置反推。
const stream = readFileSync(
  new URL("../src/hooks/useSessionStream.ts", import.meta.url),
  "utf8",
);
const viewer = readFileSync(
  new URL("../src/components/SessionViewer.tsx", import.meta.url),
  "utf8",
);

// ── 1. 编造逻辑必须不复存在（这条是整组测试存在的理由）──────────────────────
assert.doesNotMatch(
  stream,
  /inferredSeq/,
  "buildBaseTimeline 不得再按位置推断 seq —— 见本文件顶部的事故说明",
);

// ── 2. persistedSeq：真实函数体拉出来跑一遍 ────────────────────────────────
const fnStart = stream.indexOf("function persistedSeq(");
const fnEnd = stream.indexOf("\n}\n", fnStart);
assert.ok(fnStart >= 0 && fnEnd > fnStart, "persistedSeq 应该存在");
const persistedSeq = new Function(
  `${stream
    .slice(fnStart, fnEnd + 2)
    .replace(/ex: ExchangeLike/g, "ex")
    .replace(/\): number \{/, ") {")}; return persistedSeq;`,
)();

assert.equal(persistedSeq({ seq: 7 }), 7);
assert.equal(persistedSeq({ seq: "12" }), 12);
// 下面这几种就是「瞬时行」的全部形态：缺失 / 0 / 负数 / NaN / 垃圾
// —— 一律 0，绝不能是「上一条 + 1」。
assert.equal(persistedSeq({}), 0, "缺 seq 的行必须拿 0，不能拿位置");
assert.equal(persistedSeq({ seq: 0 }), 0);
assert.equal(persistedSeq({ seq: -3 }), 0, "负数 seq 不是合法身份");
assert.equal(persistedSeq({ seq: "abc" }), 0, "NaN 必须归一成 0 而不是原样透出去");
assert.equal(persistedSeq({ seq: null }), 0);
assert.equal(persistedSeq({ seq: undefined }), 0);

// ── 3. aux 只能按**真实** seq 取 ─────────────────────────────────────────
// seq 为 0 时不许落进 exchangeAux —— 否则就是认领别人的工具卡。
assert.match(
  stream,
  /const auxList = seq \? exchangeAux\[String\(seq\)\] \|\| \[\] : \[\];/,
  "seq 为 0 时必须取空 aux，最新一条真实持久行的工具卡不能被瞬时行认领",
);

// ── 4. `seq > 0` 作为「已持久化」判据的下游用途必须保留 ───────────────────
// 它们本身没错，错的是上游喂了假 seq。删掉 inferredSeq 之后这两处才真正生效。
assert.match(
  viewer,
  // 2026-10-06：判据改走共用谓词（不再是内联 `seq > 0`）—— 契约不变，且更强：
  // 现在只有一个地方判定「是不是持久行」（冲突①②）。
  /canForkAgentMessage = !isUser && isPersistedSeq\(item\.seq\)/,
  "fork 按钮只能出现在已持久化的消息上",
);
assert.match(
  stream,
  /seq: seq \|\| undefined/,
  "瞬时行的 seq 必须是 undefined（否则 data-session-seq 会渲染出一个假身份）",
);

console.log("session timeline seq: ok");
