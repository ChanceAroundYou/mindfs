// overlay 判定的**行为**测试 —— 直接 import `src/components/sessionOverlay.ts`。
//
// 这个文件存在的意义：那套判定原来住在 `SessionViewer.tsx` 的 `useMemo` 里，测不了，
// 于是 `tests/session-window.test.mjs` 只能写一份「同构副本」再对副本断言。
// 副本会漂移 —— 本文件里的四条【红】正是被副本挡住、至今没人看见的缺陷。
// （副本已在 2026-10-06 换成对真实现的薄适配器。）
//
// 命名：②③④⑭ 对应 `docs/message-mechanisms.md` §3.4 的冲突编号。
import { register } from "node:module";
import assert from "node:assert/strict";
import test from "node:test";

register("./ts-module-hook.mjs", import.meta.url);

const { computeTailOverlay, OVERLAY_DUP_MIN_CHARS } = await import(
  "../src/components/sessionOverlay.ts"
);

const row = (seq, role, content) => ({ seq, role, content });
/** 生成 seq 区间 [from, to] 的持久行，角色交替。 */
const range = (from, to) => {
  const out = [];
  for (let seq = from; seq <= to; seq += 1) {
    out.push(row(seq, seq % 2 === 0 ? "agent" : "user", `第 ${seq} 行`));
  }
  return out;
};
const seqsOf = (out) => out.map((e) => Number(e.seq || 0)).filter((n) => n > 0);

// ─────────────────────────────────────────────────────────────────────────────
// 先钉住「不该变的」：这几条是 overlay 存在的理由，改动时不许误伤。
// ─────────────────────────────────────────────────────────────────────────────
test("窗口已含的行不重复渲染；实时流式行永不丢；超出 latestSeq 的新行保留", () => {
  const visible = range(1, 5);
  const cache = [...visible, row(0, "agent", "正在流式输出的正文…"), row(6, "user", "刚发出去的")];
  const out = computeTailOverlay({ exchanges: cache, visibleExchanges: visible, latestSeq: 5 });
  assert.deepEqual(seqsOf(out), [6], "窗口已含的 1..5 不该重渲染，只有 seq>latestSeq 的 6 该保留");
  assert.equal(
    out.filter((e) => !e.seq).length,
    1,
    "seq=0 的流式正文必须保留（流式内容要实时可见）",
  );
});

test("未锚定时（latestSeq=0）不把缓存里的历史灌进 overlay", () => {
  const out = computeTailOverlay({
    exchanges: [row(3, "user", "旧")],
    visibleExchanges: range(1, 5),
    latestSeq: 0,
  });
  assert.deepEqual(seqsOf(out), [], "latestSeq 未建立时不该由 overlay 灌历史");
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】② A2 分支假定「窗口一定含 seq<=latestSeq 的行」，而它会不含
//
// 触发路径（读代码得到，不是推测）：搜索跳转 → `useSessionSearch` 设 `search_seq` →
// `App.tsx:8268` 传成 `targetSeq` → `SessionViewer` 的 `targetSeq>0 && !inRange` 分支用
// `setVisibleExchanges(winExchanges)` **整体替换**窗口，**且不推进 latestSeq**
// （`noteLatestSeq` 全文件只有 `applyWindow` 一个调用点）。
//
// 于是「被替换掉的窗口」与 latestSeq 之间那一段行：overlay 按 A2 丢弃、窗口又不含
// ⇒ **两边都不渲染**。用户看到的是「跳回旧消息后，往下滚是一片空洞」。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·②】窗口被中段替换后，被挤出去的那些持久行必须由 overlay 补渲染", () => {
  const windowNow = range(1, 10); // 跳转后的中段窗口
  const cache = range(1, 20); // 缓存里仍有完整的 1..20
  const out = computeTailOverlay({
    exchanges: cache,
    visibleExchanges: windowNow,
    latestSeq: 20, // 会话水位线仍是 20（跳转不推进也不回退它）
  });
  assert.deepEqual(
    seqsOf(out),
    [11, 12, 13, 14, 15, 16, 17, 18, 19, 20],
    "窗口只含 1..10，11..20 既不反窗口里、又被 A2 按「seq<=latestSeq」丢掉 —— 这一段谁都不渲染",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】③ 工具卡去重的两套判据在「aux 与窗口行不一致」时给出相反结论
//
// `windowToolCallIds` 是从 **visibleAux** 收集的，**不看那些 seq 是否真的在
// visibleExchanges 里**。而窗口被中段替换后，aux 与行可以脱节（`applyWindow` 的
// `{...prev, ...winAux}` / loadMore 的窗口 aux 都只按 seq 浅合并）。
// 此时 overlay 把瞬时工具卡让给「窗口侧渲染」，而窗口侧根本没有那一行 ⇒ 卡片消失。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·③】窗口 aux 里有 callId、但那一行不在窗口里时，瞬时工具卡不得让位", () => {
  const visible = range(1, 3);
  const toolRow = { role: "tool", content: "", toolCall: { callId: "call-1" } };
  const out = computeTailOverlay({
    exchanges: [toolRow],
    visibleExchanges: visible,
    visibleAux: { "99": [{ toolcall: { callId: "call-1" } }] }, // seq=99 不在窗口行里
    latestSeq: 99,
  });
  assert.equal(
    out.length,
    1,
    "让位判据只看 aux 的 callId、不看那一行是否真在窗口里 ⇒ 卡片两边都不渲染",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】④ 长度地板 32 字让**短回复**在落盘后显示两遍
//
// D 分支要求 `transientText.length >= OVERLAY_DUP_MIN_CHARS(32)` 才允许让位。
// 于是「收到」「好的」这类短回复：窗口里已有落盘那份（seq=5），缓存里那份 seq=0
// 因为太短不让位 ⇒ **同一句话显示两遍**。而短回复恰恰是最常见的一类。
// 地板原本是为了挡「同一句工程套话在不同轮次重复出现」，但那个问题该由
// 「只在窗口最新 N 行里找」（tailRows）来解决，不该靠长度地板 —— 两者是不同的事。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·④】短回复落盘后，缓存的瞬时拷贝必须让位（不得显示两遍）", () => {
  const visible = [row(4, "user", "只回复两个字：收到"), row(5, "agent", "收到")];
  const out = computeTailOverlay({
    exchanges: [...visible, row(0, "agent", "收到")],
    visibleExchanges: visible,
    latestSeq: 5,
  });
  assert.equal(
    out.filter((e) => String(e.content || "").includes("收到") && !e.seq).length,
    0,
    `窗口里已经有落盘的「收到」（seq=5），缓存里那份 seq=0 的拷贝必须让位；` +
      `现在因为短于 ${OVERLAY_DUP_MIN_CHARS} 字被放行 ⇒ 同一句话显示两遍`,
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】⑭ 用户行的两条比对判据口径不同：C 用**原始内容**、D 用**去空白**
//
// C 分支（`windowUserCounts`）按原始 content 计数，D 分支用 `normalizeOverlayText`。
// 所以「只差空白」的短用户消息：C 认不出（计数不匹配）、D 又因太短不让位 ⇒ 显示两遍。
// 同一条正文只差空白，两个判据必须给同一个答案。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑭】只差空白的短用户消息，C 与 D 的判据必须一致（不得显示两遍）", () => {
  const visible = [row(9, "user", "你好世界")];
  const out = computeTailOverlay({
    exchanges: [...visible, row(0, "user", "你好 世界")], // 只差一个空格
    visibleExchanges: visible,
    latestSeq: 9,
  });
  assert.equal(
    out.filter((e) => !e.seq && String(e.content || "").includes("世界")).length,
    0,
    "窗口里已有「你好世界」（seq=9），只差空格的 seq=0 用户拷贝必须让位 —— " +
      "C 用原始内容计数认不出它，D 又因短于长度地板放行 ⇒ 同一句话显示两遍",
  );
});
