import type { ExchangeAux, Session, SessionBlock } from "./sessionTypes";

// 「瞬时 vs 持久」的判据与 exchange 合并规则（从 services/session.ts 抽出，2026-10-07）。
//
// 这一簇是会话交换行的**单一真相**：谁该留、谁该丢、增量怎么合、加载怎么组。
// `isTransientExchange` 是其中唯一一把尺（见 docs/message-mechanisms.md 冲突①②），
// 任何地方要判「这条是不是持久行」都必须调它，不要内联 `seq > 0`。
export function getSessionMaxSeq(session: Session | null | undefined): number {
  const exchanges = Array.isArray(session?.exchanges) ? session.exchanges : [];
  return exchanges.reduce((max, exchange) => {
    const seq = Number((exchange as any)?.seq || 0);
    return Number.isFinite(seq) && seq > max ? seq : max;
  }, 0);
}

// 窗口内最小持久化 seq（seq>0），向上翻页时作为 beforeSeq 边界判定。
export function getSessionMinSeq(session: Session | null | undefined): number {
  const exchanges = Array.isArray(session?.exchanges) ? session.exchanges : [];
  let min = 0;
  for (const exchange of exchanges) {
    const seq = Number((exchange as any)?.seq || 0);
    if (Number.isFinite(seq) && seq > 0) {
      min = min === 0 ? seq : Math.min(min, seq);
    }
  }
  return min;
}

export function cloneExchangeAux(
  exchangeAux?: Record<string, ExchangeAux[]>,
): Record<string, ExchangeAux[]> {
  const out: Record<string, ExchangeAux[]> = {};
  for (const [seq, items] of Object.entries(exchangeAux || {})) {
    out[seq] = Array.isArray(items) ? [...items] : [];
  }
  return out;
}

export function toPersistentExchangeAux(
  exchangeAux?: Record<string, ExchangeAux[]>,
): Record<string, ExchangeAux[]> {
  const out: Record<string, ExchangeAux[]> = {};
  for (const [seq, items] of Object.entries(exchangeAux || {})) {
    const seqNum = Number(seq || 0);
    if (!Number.isFinite(seqNum) || seqNum <= 0) {
      continue;
    }
    const nextItems = Array.isArray(items)
      ? items.filter((item) => Number(item?.seq || 0) > 0)
      : [];
    if (nextItems.length > 0) {
      out[String(seqNum)] = nextItems;
    }
  }
  return out;
}

/**
 * 块映射的持久化清洗（CUSTOM(G-BH)）：丢掉 seq<=0 的键与块、丢掉空数组。
 *
 * 与 toPersistentExchangeAux 同形同义 —— 两者形状一致、淘汰规则一致，
 * 差别只在块的判据是 `block.seq`。住在这里而不是 sessionCacheStore.ts，
 * 是为了让「seq>0」这把尺仍然只有一个实现文件（见 message-mechanisms 冲突①②）。
 */
export function toPersistentBlocks(
  blocks?: Record<string, SessionBlock[]>,
): Record<string, SessionBlock[]> {
  const out: Record<string, SessionBlock[]> = {};
  for (const [seq, items] of Object.entries(blocks || {})) {
    const seqNum = Number(seq || 0);
    if (!Number.isFinite(seqNum) || seqNum <= 0) {
      continue;
    }
    const nextItems = Array.isArray(items)
      ? items.filter((item) => Number(item?.seq || 0) > 0)
      : [];
    if (nextItems.length > 0) {
      out[String(seqNum)] = nextItems;
    }
  }
  return out;
}

export function appendExchangeAuxDelta(
  base?: Record<string, ExchangeAux[]>,
  incoming?: Record<string, ExchangeAux[]>,
): Record<string, ExchangeAux[]> {
  const out = cloneExchangeAux(base);
  for (const [seq, items] of Object.entries(incoming || {})) {
    if (!Array.isArray(items) || items.length === 0) {
      continue;
    }
    // **幂等**：同一个 seq 下已经有的项不再追加。
    //
    // 为什么必须有这一步：增量同步的重投递是常态（游标不一致、同步中断后重跑、
    // 重锚定重复投递同一份回包），而拼接语义下第二次投递会把同一张工具卡再加一遍 ——
    // 这正是「工具卡出现两张」的机制之一（`tests/session-core-unit.test.mjs` 的【红·⑦b】）。
    //
    // 为什么是「拼接 + 去重」而不是「按 seq 覆盖」（`restoreActiveSession` 用的是覆盖）：
    // 两者是**不同的操作**，不能合并 ——
    //   · 覆盖用在**权威快照**上（窗口回包是那些 seq 的完整内容）；
    //   · 拼接用在**增量**上（同一个 seq 会陆续追加新的 thought/tool 项，覆盖会把先到的丢掉）。
    // 一度以为该统一成覆盖，核对调用方后确认那是错的：覆盖会让增量路径丢卡片。
    // ponytail: 去重键是整项的 JSON（同源产出，键序一致）。跨源比较若出现键序差异会漏判 ——
    // 那时再上递归键排序；现在没有跨源比较的调用方。
    const key = (item: ExchangeAux) => JSON.stringify(item);
    const seen = new Set((out[seq] || []).map(key));
    const fresh = items.filter((item) => {
      const k = key(item);
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
    if (fresh.length > 0) {
      out[seq] = [...(out[seq] || []), ...fresh];
    }
  }
  return out;
}

function preferIncomingText(next?: string, prev?: string) {
  const normalizedNext = (next || "").trim();
  if (normalizedNext) {
    return next;
  }
  return prev;
}

export function withSessionMeta(
  base: Session | null | undefined,
  incoming: Session | null | undefined,
): Session | null {
  if (!base && !incoming) {
    return null;
  }
  if (!base) {
    return incoming
      ? {
          ...incoming,
          exchanges: Array.isArray(incoming.exchanges)
            ? [...incoming.exchanges]
            : [],
        }
      : null;
  }
  if (!incoming) {
    return {
      ...base,
      exchanges: Array.isArray(base.exchanges) ? [...base.exchanges] : [],
    };
  }
  return {
    ...base,
    ...incoming,
    agent: preferIncomingText(incoming.agent, base.agent),
    model: preferIncomingText((incoming as any).model, (base as any).model),
    mode: preferIncomingText((incoming as any).mode, (base as any).mode),
    effort: preferIncomingText((incoming as any).effort, (base as any).effort),
    fast_service:
      typeof (incoming as any).fast_service === "string"
        ? (incoming as any).fast_service
        : typeof (base as any).fast_service === "string"
          ? (base as any).fast_service
          : "",
    plan_mode:
      typeof (incoming as any).plan_mode === "boolean"
        ? (incoming as any).plan_mode
        : !!(base as any).plan_mode,
    name: preferIncomingText(incoming.name, base.name) || "",
    exchanges: Array.isArray(incoming.exchanges) ? [...incoming.exchanges] : [],
    exchange_aux: cloneExchangeAux(incoming.exchange_aux || base.exchange_aux),
  };
}

/**
 * 按 seq 合并两份持久化 exchanges（**丢弃 seq=0 的瞬时行**，那些归 overlay 管）。
 *
 * 为什么必须去重而不是拼接：三条来源会重叠。
 *   - 缓存（用户翻上去翻出来的历史 / live 路径落库的行）
 *   - 服务端尾部窗口（latest=SESSION_WINDOW_SIZE）
 *   - loadMore 取的 beforeSeq 段
 * 纯拼接会把同一行渲染两遍（实测 2026-10-05：切会话后历史里每条消息出现两次）。
 *
 * 同 seq 冲突时让**后到的赢** —— 三个调用点上前者都是更新的那份。顺带按 seq
 * 升序排，调用方不必自己维护顺序。
 *
 * 淘汰与清理只发生在**写入**时（见 toPersistentSession，只从头淘汰）；
 * 加载过程中一律只增不减 —— 用户正在看的那段历史不该在读的时候被砍掉。
 */
export function mergeSessionExchanges(
  base: readonly any[] | null | undefined,
  incoming: readonly any[] | null | undefined,
): any[] {
  // 判据走**共用的** `isTransientExchange`，不再内联 `seq > 0`。
  // 两把尺的教训（冲突①②）：`dropTransientExchanges` 用谓词、这里内联 `seq > 0`，
  // 于是「用户行没有 seq」这件事两边判法不同、去留取决于先跑哪条路径。
  // 只要这里再内联一次，那条 bug 就能悄悄回来 —— 所以判据只能有一处实现。
  const merged = new Map<number, any>();
  for (const ex of [...(base || []), ...(incoming || [])]) {
    if (isTransientExchange(ex)) continue;
    merged.set(Number((ex as any)?.seq || 0), ex);
  }
  return [...merged.values()].sort(
    (a, b) => Number((a as any)?.seq || 0) - Number((b as any)?.seq || 0),
  );
}

/**
 * 会话加载的**唯一**组装规则。
 *
 * `serverExs`：服务端刚回来的行（窗口回包，或 syncSession 的结果）—— 只保证含已落盘行。
 * `cachedExs`：加载前内存缓存里的内容。
 * `inFlight`：这条会话**此刻是否还有在途回合**（即 `pending`）。
 *
 * 瞬时行（seq=0）是「还没落库的内容」，只有会话在跑的时候才可能是真的在途内容。
 * 会话不在跑时，缓存里的 seq=0 全是残留：一轮结束时服务端已经把它们落盘了，
 * 而 compact 会让服务端历史整个重置，本地那份就永远对不上。这类残留会跨多次 compact
 * 无声堆积（实测一个会话堆到 1100+ 条，其中 tool=700、thought=360），一旦被渲染出来
 * 就是「同一段正文出现两遍、而且每切一次越多」。
 *
 * 所以判据只有一条：**不在跑就不留**。不需要轮次标记之类的东西 ——
 * 「此刻在不在跑」本来就已知，没必要为了清理再给每一行记一个身份。
 *
 * 另一半教训：瞬时行**绝不能去服务端回包找**（那里一个 seq=0 都没有）。曾经有三个
 * 地方各写了一遍这个判断、三处都去服务端回包找，于是每次加载都把在途内容丢一次。
 * 症状是切回**运行中**的会话时「完整对话 → 塌到最后一条用户 prompt → 再慢慢补回来」；
 * 已结束的会话没有在途内容，怎么切都不闪 —— 这是它长期难以复现的原因。
 */
export function composeLoadedExchanges(
  serverExs: readonly any[] | null | undefined,
  cachedExs: readonly any[] | null | undefined,
  inFlight: boolean,
): any[] {
  const persisted = mergeSessionExchanges(cachedExs, serverExs);
  if (!inFlight) {
    return persisted;
  }
  // 在途：把 seq=0 接回。取并集（缓存在前 —— 它才是在途内容的权威来源），按对象
  // 同一性去重：同一个 exchange 可能两侧都有（服务端把本地刚发的那条也回传了），
  // 不按引用去重会渲染两遍。
  const transient: any[] = [];
  const seen = new Set<unknown>();
  for (const ex of [...(cachedExs || []), ...(serverExs || [])]) {
    // 同样走谓词：`!== 0` 会漏掉「seq 字段缺失」的行，而那类行按定义也是瞬时行
    // （`isTransientExchange` 把缺字段/非数字一律算瞬时）。
    if (!isTransientExchange(ex)) continue;
    if (seen.has(ex)) continue;
    seen.add(ex);
    transient.push(ex);
  }
  // **compact 边界规则已删除（2026-10-06）** —— 它的前提是假的。
  //
  // 原注释说「compact 是服务端历史的重置点，在它之前产生的瞬时行属于服务端已经丢掉的那段
  // 历史」。核对服务端：交换 JSONL 是 **append-only**，全仓唯一的 `os.Remove` 落在两处
  // `DeleteSession`（`manager.go:1439/:1447`），compact 只写一条 `CompactNotice` aux
  // （`usecase/session.go:2652`）—— **一行历史都没丢**。它重置的是 agent 的上下文窗口，
  // 不是磁盘上的交换日志。
  //
  // 而该规则会造成真实的内容丢失：**本轮跑到一半发生 compact 时**，本轮 compact 之前
  // 产生的那些行被无条件丢弃，而它们在服务端**没有任何替代品**（本轮的助手行要到回合末
  // 才落盘）⇒ 用户在切回/重锚定时看到「本轮前半段消失」。见
  // `tests/session-core-unit.test.mjs` 的【红·⑨b】，以及 `docs/message-mechanisms.md` 冲突⑨。
  //
  // 它当初想解决的「跨多次 compact 堆到 1100+ 条」，正解是**主清理**：
  // 一轮结束 / reset / compact 时由 `dropTransientExchanges` 清瞬时行（那三处调用点已在）。
  // 「不在跑就没在途内容」这一条判据就够了，不需要再给每一行记轮次身份。
  return [...persisted, ...transient];
}

/**
 * 这一行是不是「服务端也持有的内容」。
 *
 * **瞬时行 = `seq` 为空。**（2026-10-06 起用户行不再例外）
 *
 * 用户行曾经被排除（怕刚发出去的消息闪一下），但那条例外造成了**两把尺**：
 * `dropTransientExchanges` 保留 seq=0 用户行，`mergeSessionExchanges` 只收 seq>0 丢弃它。
 * 同一条行的去留因此取决于「先跑哪条路径」。现在两边都丢：丢的那一刻它已有替代品
 * （用户行在回合开始就落盘；reset 在窗口装好之后），而保留它的代价是认领漏掉时
 * 与服务端持久行同时渲染。详见 `isTransientExchange` 的注释。
 */
/**
 * 把会话里所有**乐观回声**标记为已确认（`pending_ack: true → false`）。
 *
 * 这个形状此前在 `App.tsx` 与 `useRealtimeEvents.ts` 各写了一份**完全一样**的局部
 * `clearPendingAck`（冲突⑥）。收敛到一处：改判据时必须只改这里。
 */
export function settlePendingAcks<T>(session: T): T {
  const exchanges = (session as any)?.exchanges;
  if (!Array.isArray(exchanges)) return session;
  let changed = false;
  const next = exchanges.map((exchange: any) => {
    if (exchange?.pending_ack !== true) return exchange;
    changed = true;
    return { ...exchange, pending_ack: false };
  });
  return changed ? ({ ...(session as any), exchanges: next } as T) : session;
}

/** 这条待确认的发送与那条乐观回声是不是同一条（`content` + `timestamp` 都对得上）。 */
export function isSamePendingEcho(
  exchange: any,
  pending: { message?: string; timestamp?: string } | null | undefined,
): boolean {
  if (!pending || exchange?.pending_ack !== true) return false;
  return exchange.content === pending.message && exchange.timestamp === pending.timestamp;
}

export function isTransientExchange(exchange: unknown): boolean {
  const ex = exchange as any;
  if (Number(ex?.seq || 0) > 0) return false;
  // **用户行不再例外**（2026-10-06）。曾经这里排除 user 行，理由是「乐观回声没有 seq，
  // 照此清理会让刚发出去的消息当场消失」。但那条理由站不住，而且造成了**两把尺**：
  //   · `dropTransientExchanges`（done / reset 两处调用）→ 保留 seq=0 用户行；
  //   · `mergeSessionExchanges`（每次加载）→ 只收 seq>0 ⇒ **丢弃**它。
  // 同一条行的去留因此取决于「先跑哪条路径」，正是 `docs/message-mechanisms.md` 冲突①。
  //
  // 取「都丢弃」这个方向是安全的，因为丢的那一刻它已经有替代品：
  //   · 用户行在**回合开始**就落盘（`4673ee4`），done 时服务端窗口里一定有它；
  //   · `reset` 发生在快照/窗口装好**之后**（`restoreActiveSession` 的顺序）。
  // 而保留它的代价是真实的：认领漏掉时（WS 断连等）会与服务端的持久行**同时渲染**——
  // 那是「同一句用户消息两遍」的直接来源。
  // 乐观回声的可见期因此收敛为「发出 → 被认领（拿到真 seq）」，回归护栏见 E2E 的
  // 「用户消息在时间线里恰好出现 1 次」。
  return true;
}

/**
 * 这个 seq 是否代表「已持久化」。**与 `isTransientExchange` 同源** ——
 * 存在的意义就是别再让别处内联 `seq > 0`（那是第二把尺，见冲突①②）。
 * 接受 `unknown` 是因为调用点常常只有一个 `seq` 值（TimelineItem / 裸数字）。
 */
export function isPersistedSeq(seq: unknown): boolean {
  return !isTransientExchange({ seq });
}

/**
 * 丢弃会话里的瞬时行。
 *
 * **挂载会话时的落点**：服务端在 `session.ready` 后回一帧带 `reset:true` 的整批快照，
 * 客户端先调本函数清空瞬时尾巴，再把快照事件原样应用一遍。于是
 * 「客户端瞬时尾巴 = 服务端 buffer 的纯函数」—— 与它此前见过什么、有没有漏收无关，
 * 重放 N 次结果逐字节相同。重复渲染那一整类 bug 由此在结构上消失。
 *
 * 另有两个调用点（一轮结束时、compact 时）：那时服务端已把内容落盘或整个重置，
 * 缓存里剩下的 seq=0 是陈旧拷贝。
 *
 * 队列续跑的情况**不要调**：那时新一轮的瞬时行正在产生。
 */
export function dropTransientExchanges(
  session: Session | null | undefined,
): Session | null {
  if (!session || !Array.isArray((session as any).exchanges)) {
    return (session as Session) ?? null;
  }
  const before = (session as any).exchanges as any[];
  const kept = before.filter((ex) => !isTransientExchange(ex));
  if (kept.length === before.length) return session as Session;
  return { ...(session as any), exchanges: kept } as Session;
}

export function appendSessionDelta(
  base: Session | null | undefined,
  incoming: Session | null | undefined,
): Session | null {
  const baseWithMeta = withSessionMeta(base, incoming);
  if (!baseWithMeta) {
    return null;
  }
  const baseExchanges = Array.isArray(base?.exchanges)
    ? base.exchanges.filter(
        (exchange) => Number((exchange as any)?.seq || 0) > 0,
      )
    : [];
  // incoming 不过滤 seq=0：sync/GET 会以 seq=0 下发「尚未落库的 pending 条目」。
  // 注意：正在等待回答的 ask_user 卡**不在此列** —— 它是纯内存态（服务端
  // manager.pendingToolCalls + 前端 sessionCacheRef），服务端从不经 HTTP 下发
  // （session.Exchange 结构体没有 ToolCall 字段）。
  //
  // **base 过滤 seq>0 是刻意的，不是漏了合并**：base 是 IDB 里的持久化记录，在途内容
  // 不在其中；这里的增量合并只负责「已落盘部分」。内存里那份在途内容由调用方经
  // composeLoadedExchanges 接回 —— 那条才是「加载时不丢在途内容」的唯一出口。
  // 别在这里补 seq=0 的合并：base 是持久层，把瞬时行写进 IDB 是另一个错误。
  const incomingExchanges = Array.isArray(incoming?.exchanges)
    ? (incoming.exchanges as any[])
    : [];
  const baseExchangeAux = toPersistentExchangeAux(base?.exchange_aux);
  const incomingExchangeAux = toPersistentExchangeAux(incoming?.exchange_aux);
  return {
    ...baseWithMeta,
    exchanges: mergeSessionExchanges(baseExchanges, incomingExchanges),
    exchange_aux: appendExchangeAuxDelta(baseExchangeAux, incomingExchangeAux),
  };
}
