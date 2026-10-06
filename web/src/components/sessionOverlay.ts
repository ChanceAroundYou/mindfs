// overlay 尾巴的**纯判定** —— 从 `SessionViewer.tsx` 的 `useMemo` 里原样搬出来的。
//
// ## 为什么必须搬出来（不是为了好看）
//
// 它原来住在组件里，因此**测不了**。测试只能各自想办法绕：
//   · `tests/session-window.test.mjs:778` 起写了一份「与 SessionViewer.tailOverlay **同构**」
//     的副本实现，再对副本断言；
//   · `tests/session-window-overlay-dedup.test.mjs` 对 `composedExchanges` 那段源码做
//     字符串手术（替换 `useMemo(` → `return (`）再 eval。
//
// 两份绕法都是**会漂移的副本** —— 而 `docs/message-mechanisms.md` §3.4 冲突②（渲染空洞）
// 恰恰是那份副本挡住了的真实缺陷：A2 分支假定「seq<=latestSeq ⇒ 窗口会渲染它」，
// 而 `targetSeq` 跳转会用中段窗口整体替换 `visibleExchanges` 且不推进 `latestSeq`，
// 于是那段区间两边都不渲染。副本里只复刻了作者关心的分支，所以它一直是绿的。
//
// 搬出来之后：测试直接 import 本模块，副本可以删掉；判定逻辑也只有一处。
//
// ## 契约边界
//
// 本模块**不改任何行为** —— 逐字搬迁，只把两个阈值变成可选参数（默认值与原常量相同），
// 好让测试能确定性地构造边界。判断依据全是「调用方喂进来的状态」，不读 React、不读缓存。
/** 一行 exchange 的最小形状。真实类型在 `services/session.ts` 的 `Exchange`。 */
export type OverlayExchange = { seq?: number | string | null; role?: string; content?: string } & Record<
  string,
  unknown
>;

export interface TailOverlayInput {
  /** App 缓存里的行（`session.exchanges`，**含** seq=0 的瞬时行）。 */
  exchanges?: readonly OverlayExchange[] | null;
  /** 窗口态：只含已持久化的行（`visibleExchanges`）。 */
  visibleExchanges?: readonly OverlayExchange[] | null;
  /** 窗口态的 aux，按 seq 索引（`visibleAux`）—— 用来判断工具卡的 callId 是否已由窗口渲染。 */
  visibleAux?: Record<string, readonly { toolcall?: { callId?: string } }[]> | null;
}

/** overlay 对账用的归一化：只去空白。
 *
 * 服务端落盘时会在相邻文本块之间补 `"\n\n"`（`usecase.appendResponseChunk`），
 * 而流式 `message_chunk` 不带，所以同一轮的「缓存瞬时拷贝」与「落盘正文」只差空白 ——
 * 逐字比较认不出来，去空白才认得出。 */
export function normalizeOverlayText(value: string): string {
  return value.replace(/\s+/g, "");
}

const seqOf = (ex: OverlayExchange | undefined): number => Number((ex as any)?.seq || 0);
const roleOf = (ex: OverlayExchange | undefined): string =>
  String((ex as any)?.role || "").toLowerCase();
const contentOf = (ex: OverlayExchange | undefined): string => String((ex as any)?.content || "");

/**
 * 算出「窗口尚未覆盖、需要 overlay 补渲染」的那些行。
 *
 * ## 分支一览（2026-10-06 简化后 —— 合上游时逐条核对）
 *
 * **P 持久行：只有一条规则** —— `visibleSeqSet.has(seq)` 就跳过，否则渲染。
 *   曾经还有第二条：「`seq <= latestSeq` 也跳过」，理由是「窗口一定含它或即将含它」。
 *   那个假设不成立：`latestSeq` 只被「最新窗口」推进（`noteLatestSeq` 全文件一个调用点），
 *   而搜索跳转走的 `targetSeq` 分支会用**中段窗口整体替换** `visibleExchanges` 且不碰它
 *   ⇒ 两者之间那段行「窗口没有、又被水位线判定为已有」⇒ **谁都不渲染**（渲染空洞，冲突②）。
 *   判据必须是「窗口**是否真的含**这一行」，而不是「水位线到没到」。
 *
 * **T 瞬时 tool 行：无条件保留**，交给下游的 `dedupeToolCards` 去重。
 *   曾经这里还查一遍「窗口 aux 里有没有同 callId 就让位」，与 `dedupeToolCards` 是同一
 *   concern 的两套判据（冲突③）：aux 只看 callId、不看那一行是否真在窗口里，
 *   窗口被中段替换后会两边都不渲染。现在只剩一处（时间线里，窗口在前故保留持久那份）。
 *
 * **U 瞬时 user 行：与窗口里同内容（归一化后）的计数消抵** —— 跨端丢事件的兜底。
 *   归一化是必须的：流式 `message_chunk` 不带落盘时补的 `"\n\n"`，逐字比会漏（冲突⑭）。
 *
 * **A 其余瞬时行（正文/思考）：与窗口里「本轮」的持久行比对，被包含就让位。**
 *   两处限定都不能少：
 *   · **限定在本轮**（窗口里 `seq > 最后一条 user 行的 seq` 的那些持久行）：跨轮比对会误伤 ——
 *     同一句工程套话（「好的」「继续」）在不同轮次重复出现是正常的；
 *   · **归一化后包含**：服务端 `appendResponseChunk` 会在相邻文本块之间补 `"\n\n"`，
 *     流式 chunk 不带，所以同一轮的两份只差空白；而落盘那份往往**更长**（多出收尾段），
 *     故是「包含」不是「相等」。
 *   曾经的三个补丁已删除：≥32 字长度地板（短回复落盘后显示两遍，冲突④）、只扫窗口最新 3 行
 *   （更早的本轮行认不出）、以及裸子串比对（会把「好的」这类误判）。长度地板的原意
 *   「挡掉合法重复」现在由「限定在本轮」承担，不需要再靠长度。
 *
 * **E 兜底：保留**（流式内容要实时可见）。
 */
export function computeTailOverlay(input: TailOverlayInput): OverlayExchange[] {
  const exs = Array.isArray(input.exchanges) ? (input.exchanges as OverlayExchange[]) : [];
  const visible = Array.isArray(input.visibleExchanges)
    ? (input.visibleExchanges as OverlayExchange[])
    : [];

  // ── 窗口侧派生 ───────────────────────────────────────────────────────────────
  const visibleSeqSet = new Set<number>();
  let lastVisibleUserSeq = 0;
  for (const ex of visible) {
    const seq = seqOf(ex);
    if (seq > 0) visibleSeqSet.add(seq);
    if (seq > 0 && roleOf(ex) === "user" && seq > lastVisibleUserSeq) {
      lastVisibleUserSeq = seq;
    }
  }
  // 「本轮已经会被渲染的持久正文」= 最后一条 user 行**之后**的持久行，归一化后收集。
  // 两个来源都要收：
  //   · 窗口里那些（窗口侧渲染）；
  //   · **缓存里那些窗口还没含的**（下面 P 分支会由 overlay 自己渲染）—— 漏掉它们，
  //     刚落盘、还没进窗口的那一轮就会被显示两遍（实测 2026-09-17 20:16 的缺口）。
  // 只收本轮：跨轮比对会误伤（同一句「好的」在不同轮次重复出现是正常的）。
  const turnPersistedTexts: string[] = [];
  if (lastVisibleUserSeq > 0) {
    for (const ex of [...visible, ...exs]) {
      if (seqOf(ex) <= lastVisibleUserSeq) continue;
      const text = normalizeOverlayText(contentOf(ex));
      if (text) turnPersistedTexts.push(text);
    }
  }
  const windowUserCounts = new Map<string, number>();
  for (const ex of visible) {
    if (roleOf(ex) !== "user") continue;
    const content = normalizeOverlayText(contentOf(ex));
    windowUserCounts.set(content, (windowUserCounts.get(content) || 0) + 1);
  }

  const consumed = new Map<string, number>();
  const out: OverlayExchange[] = [];
  for (const ex of exs) {
    const seq = seqOf(ex);
    if (seq > 0) {
      // P：**唯一**的持久行规则 —— 窗口真的含它才跳过
      if (visibleSeqSet.has(seq)) continue;
      out.push(ex);
      continue;
    }
    // T：瞬时 tool 行无条件保留（去重交给 dedupeToolCards）
    if (roleOf(ex) === "tool") {
      out.push(ex);
      continue;
    }
    // U：瞬时 user 行与窗口计数消抵（归一化后比较）
    if (roleOf(ex) === "user") {
      const content = normalizeOverlayText(contentOf(ex));
      const covered = windowUserCounts.get(content) || 0;
      const used = consumed.get(content) || 0;
      if (used < covered) {
        consumed.set(content, used + 1);
        continue;
      }
      out.push(ex);
      continue;
    }
    // A：与窗口里本轮的持久正文比对（归一化后包含）⇒ 这一份是陈旧投影，作废
    const transientText = normalizeOverlayText(contentOf(ex));
    if (transientText && turnPersistedTexts.some((text) => text.includes(transientText))) {
      continue;
    }
    // E
    out.push(ex);
  }
  return out;
}
