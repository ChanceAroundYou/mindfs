import type { ExchangeAux, RelatedFile, Session, SessionBlock } from "./sessionTypes";
import type { CachedSessionListRecord, CachedSessionRecord } from "./sessionCacheTypes";
import {
  buildSessionCacheKey,
  buildSessionListCacheKey,
  sessionRequestToPromise,
  withSessionListStore,
  withSessionStore,
} from "./sessionIndexedDb";
import { cloneExchangeAux, toPersistentBlocks, toPersistentExchangeAux } from "./sessionExchangeMerge";

// 单会话的 IndexedDB 缓存读写 + 落盘裁剪（从 services/session.ts 抽出，2026-10-07）。
//
// 这一簇管的是「一条会话怎么存、怎么取」：写入前裁到预算（toPersistentSession），
// 读出前剥掉只在内存里有意义的字段（stripAnchorBookkeeping），增删改走同一个 store。
// 合并/判据那一侧在 sessionExchangeMerge.ts，数据库开关与账户作用域在 sessionIndexedDb.ts。
async function loadCachedSession(
  rootId: string,
  sessionKey: string,
  nodeId?: string,
): Promise<Session | null> {
  try {
    const record = await withSessionStore("readonly", (store) =>
      sessionRequestToPromise(
        store.get(buildSessionCacheKey(rootId, sessionKey, nodeId)) as IDBRequest<
          CachedSessionRecord | undefined
        >,
      ),
    );
    return record?.session || null;
  } catch {
    return null;
  }
}

export async function saveCachedSession(
  rootId: string,
  session: Session | null | undefined,
  nodeId?: string,
): Promise<void> {
  if (!rootId || !session?.key) {
    return;
  }
  const persistentSession = toPersistentSession(session);
  const record: CachedSessionRecord = {
    cacheKey: buildSessionCacheKey(rootId, session.key, nodeId),
    rootId,
    nodeId: String(nodeId || "").trim() || undefined,
    sessionKey: session.key,
    touchedAt: Date.now(),
    session: persistentSession,
  };
  try {
    await withSessionStore("readwrite", (store) =>
      sessionRequestToPromise(store.put(record)),
    );
  } catch {}
}

export async function deleteCachedSession(
  rootId: string,
  sessionKey: string,
  nodeId?: string,
): Promise<void> {
  try {
    await withSessionStore("readwrite", (store) =>
      sessionRequestToPromise(
        store.delete(buildSessionCacheKey(rootId, sessionKey, nodeId)),
      ),
    );
  } catch {}
}

/**
 * 只删**列表快照**，不动单会话记录。
 * 删除会话后必须调用：列表快照是下次进面板时**先渲染、后请求**的来源，
 * 不同步失效的话已删的行会从 IndexedDB 里重新出现（单个会话记录删了也没用）。
 */
export async function deleteCachedSessionLists(
  rootId: string,
  nodeId?: string,
): Promise<void> {
  if (!rootId) {
    return;
  }
  const nid = String(nodeId || "").trim();
  try {
    await withSessionListStore("readwrite", async (store) => {
      try { await sessionRequestToPromise(store.delete(buildSessionListCacheKey(rootId, nid || undefined))); } catch {}
      if (nid) {
        // node-blind 旧键：不带 nodeId 的历史记录
        try { await sessionRequestToPromise(store.delete(buildSessionListCacheKey(rootId))); } catch {}
      } else {
        const entries = (await sessionRequestToPromise(store.getAll() as IDBRequest<CachedSessionListRecord<any>[]>)) || [];
        for (const entry of entries) {
          const key = String(entry?.cacheKey || "");
          if (key === `root::${rootId}` || key.endsWith(`::${rootId}`)) {
            try { await sessionRequestToPromise(store.delete(key)); } catch {}
          }
        }
      }
      // 多项目面板按账户缓存的是这份快照，但按 G-BJ 已整体退役，无需再失效。
    });
  } catch {}
}

export async function clearCachedSessionsForRoot(
  rootId: string,
  nodeId?: string,
): Promise<void> {
  if (!rootId) {
    return;
  }
  const nid = String(nodeId || "").trim();
  try {
    await withSessionStore("readwrite", async (store) => {
      const entries =
        (await sessionRequestToPromise(
          store.getAll() as IDBRequest<CachedSessionRecord[]>,
        )) || [];
      await Promise.all(
        entries
          // 未传 nodeId 时清整棵 root（历史行为）；传了则只清该节点，
          // 并顺带删除遗留的 node-blind 旧键（无 nodeId 字段的记录）
          .filter(
            (record) =>
              record.rootId === rootId &&
              (!nid || record.nodeId === nid || !record.nodeId),
          )
          .map((record) => sessionRequestToPromise(store.delete(record.cacheKey))),
      );
    });
    await withSessionListStore("readwrite", async (store) => {
      if (nid) {
        try { await sessionRequestToPromise(store.delete(buildSessionListCacheKey(rootId, nid))); } catch {}
        try { await sessionRequestToPromise(store.delete(buildSessionListCacheKey(rootId))); } catch {}
      } else {
        const entries = (await sessionRequestToPromise(store.getAll() as IDBRequest<CachedSessionListRecord<any>[]>) ) || [];
        for (const entry of entries) {
          const key = String(entry?.cacheKey || "");
          if (key === `root::${rootId}` || key.endsWith(`::${rootId}`)) {
            try { await sessionRequestToPromise(store.delete(key)); } catch {}
          }
        }
        try { await sessionRequestToPromise(store.delete(buildSessionListCacheKey(rootId))); } catch {}
      }
    });
  } catch {}
}

export function cloneSession(session: Session): Session {
  return {
    ...session,
    related_files: Array.isArray(session.related_files)
      ? [...session.related_files]
      : [],
    exchanges: Array.isArray(session.exchanges) ? [...session.exchanges] : [],
    exchange_aux: cloneExchangeAux(session.exchange_aux),
  };
}

const SESSION_CACHE_MAX_EXCHANGES = 500;
const SESSION_CACHE_MAX_TEXT = 200 * 1024;

/**
 * 截断到 meta + 文本/条数上限——避免每次打开大 session 把整份 JSONL（可达 MB 级）
 * 全量写进 IndexedDB 卡主线程。截断时置 truncated 标记，读取方下次以全量拉取补段
 * （见 syncSession）。
 *
 * **只从头部淘汰，永远保尾部**（用户定的规则）。尾部是用户正在看的那一段；把最新的
 * 消息丢掉不只是「少了几条」，而是首帧从中间开始、读起来像会话断在中间。
 *
 * 曾经的写法反了：它从**尾部**往前扫文本、把超预算的条数记下来，再
 * `slice(0, len - n)` —— 砍掉的正是最新的那几条（超预算的通常是最长的助手回答）。
 * 实测症状：会话越长、开得越久，尾部丢得越多。
 */
export function toPersistentSession(session: Session): Session {
  const persistent = stripAnchorBookkeeping(session);
  const exchanges = Array.isArray(persistent.exchanges)
    ? persistent.exchanges.filter((exchange) => {
        const seq = Number((exchange as any)?.seq || 0);
        return Number.isFinite(seq) && seq > 0;
      })
    : [];
  const exchange_aux = toPersistentExchangeAux(persistent.exchange_aux);
  // 正文块（CUSTOM(G-BH)）的持久化形：不进淘汰预算（它就是正文本身，与 exchanges 同生共死），
  // 但「只在册 seq」这条规则与 aux 一致 —— 否则淘汰后孤立的块会在时间线上多出内容。
  const blocks = toPersistentBlocks(persistent.blocks);
  if (exchanges.length <= SESSION_CACHE_MAX_EXCHANGES) {
    let text = 0;
    for (const exchange of exchanges) {
      text += String((exchange as any)?.content || "").length;
    }
    if (text <= SESSION_CACHE_MAX_TEXT) {
      // 没有淘汰 ⇒ 这条记录此刻是完整的。显式写 false（而不是留 `...persistent` 里的旧值）：
      // 旧值可能是上一轮淘汰留下的 true，留着会让下次白做一次全量拉取。
      return { ...persistent, truncated: false, exchanges, exchange_aux, blocks };
    }
  }
  // 从尾部往前累加，找到「能装下的最长后缀」；装不下的部分全在头部。
  //
  // 两个坑（都实测过）：
  //   ① 扫描下界必须是**独立的** floorStart，不能复用会变的 start —— 一旦
  //      start=len-1，条件 `i >= start` 立刻为假，循环只跑一轮、只留下最新那一条。
  //   ② 最新一条自己就超预算时不能 break 了事（那会让 start 停在 len-1、白留
  //      一整份超预算的尾巴）；此时直接不保留任何一条 —— 全量回源才是正解，
  //      而 truncated 标记已经保证下次会全量拉。
  const floorStart = Math.max(0, exchanges.length - SESSION_CACHE_MAX_EXCHANGES);
  let text = 0;
  let keptFrom = exchanges.length;
  for (let i = exchanges.length - 1; i >= floorStart; i -= 1) {
    const itemText = String((exchanges as any)[i]?.content || "").length;
    // 单条就超预算时 keptFrom 停在 length（初值）→ 一条不留，下次靠 truncated 全量回源。
    if (text + itemText > SESSION_CACHE_MAX_TEXT) break;
    text += itemText;
    keptFrom = i;
  }
  const kept = exchanges.slice(keptFrom);
  // aux 只保留下**还在 exchanges 里**的那些 seq：淘汰掉的头部行不该留下工具卡数据
  // —— 否则重新加载时那批 aux 会孤立存在，构建时间线时凭空多出卡片。
  const keptSeqs = new Set(kept.map((ex) => Number((ex as any)?.seq || 0)));
  const keptAux: Record<string, ExchangeAux[]> = {};
  for (const [seq, items] of Object.entries(exchange_aux)) {
    if (keptSeqs.has(Number(seq))) keptAux[seq] = items;
  }
  // 块随 aux 一起收窄到在册 seq（CUSTOM(G-BH)）。
  const keptBlocks: Record<string, SessionBlock[]> = {};
  for (const [seq, items] of Object.entries(blocks)) {
    if (keptSeqs.has(Number(seq))) keptBlocks[seq] = items;
  }
  // **发生过淘汰就一定标 true** —— 这个标记的语义是「这条 IDB 记录不完整，下次必须全量回源」。
  // 曾经它被调用方用 `forceNoTruncate` 置成 false（窗口态写入时），注释说是为了「跳过
  // truncated 全量回补」。但那是**读侧**该管的事（`syncSession` 里已有的
  // `baseTruncated = windowed ? false : base.truncated`），写在写侧等于让标记说谎：
  // 窗口态标记是**内存**的，页面一刷新就没了，而这条被标成「完整」的记录还在盘上 ——
  // 下一次全量路径读到它就只做增量同步，**被砍掉的那段历史永远不补**（静默丢历史）。
  return {
    ...persistent,
    truncated: true,
    exchanges: kept,
    exchange_aux: keptAux,
    blocks: keptBlocks,
  };
}

// stripAnchorBookkeeping 丢弃 SessionViewer 的重锚定簿记字段，避免其落 IndexedDB。
//
// _windowMeta/_anchoredAt 只在内存里对「当前会话实例」有意义（App 换窗时递增 _anchoredAt，
// 视图侧按它做一次性原子换窗）。一旦持久化，冷启动时 SessionViewer 的 lastAppliedAnchorRef
// 从 {key:null, at:-1} 开始，陈旧的 _anchoredAt 会通过守卫（at >= -1）被误当成新锚点应用，
// 而 applyWindow 是把 session.exchanges **原样**当作窗口渲染的 —— 于是「持久化下来的旧
// exchange 集」被当成当前窗口显示，表现为刷新后旧内容出现在当前位置（实测 2026-09-12 症状 3）。
//
// 只剥离这两个字段：_nodeId 仍要保留（SessionViewer 取窗口时用它做节点路由）。
export function stripAnchorBookkeeping(session: Session): Session {
  const raw = session as any;
  if (!("_windowMeta" in raw) && !("_anchoredAt" in raw)) {
    return session;
  }
  const { _windowMeta, _anchoredAt, ...rest } = raw;
  void _windowMeta;
  void _anchoredAt;
  return rest as Session;
}

export async function getCachedSession(
  rootId: string,
  sessionKey: string,
  nodeId?: string,
): Promise<Session | null> {
  const cached = await loadCachedSession(rootId, sessionKey, nodeId);
  return cached ? cloneSession(cached) : null;
}

export async function setCachedSessionRelatedFiles(
  rootId: string,
  sessionKey: string,
  relatedFiles: RelatedFile[],
  nodeId?: string,
): Promise<Session | null> {
  const cached = await loadCachedSession(rootId, sessionKey, nodeId);
  if (!cached) {
    return null;
  }
  const next: Session = {
    ...cached,
    related_files: Array.isArray(relatedFiles) ? [...relatedFiles] : [],
  };
  await saveCachedSession(rootId, next, nodeId);
  return cloneSession(next);
}
