import { useCallback, useEffect } from "react";
import type { Dispatch, MutableRefObject, RefObject, SetStateAction } from "react";
import {
  SESSION_WINDOW_SIZE,
  clearWindowedView,
  getSessionWindow,
  isPersistedSeq,
  mergeSessionExchanges,
  setWindowedView,
  type ExchangeAux,
  type SessionWindowMeta,
} from "../../services/session";
import type { ExchangeArray, SessionItem } from "./sessionViewerTypes";

export type SessionWindowingCtx = {
  session: SessionItem | null;
  sessionKey: string | null;
  sessionNodeId: string | undefined;
  rootId?: string | null;
  windowMeta: SessionWindowMeta;
  loadingMore: boolean;
  applyWindow: (
    res: { session: any; meta: SessionWindowMeta } | null,
    opts?: { keepOlder?: boolean },
  ) => boolean | void;
  setVisibleExchanges: Dispatch<SetStateAction<ExchangeArray>>;
  setVisibleAux: Dispatch<SetStateAction<Record<string, ExchangeAux[]>>>;
  setWindowMeta: Dispatch<SetStateAction<SessionWindowMeta>>;
  setLoadingMore: Dispatch<SetStateAction<boolean>>;
  setShowJumpToLatest: Dispatch<SetStateAction<boolean>>;
  shouldStickToBottomRef: MutableRefObject<boolean>;
  isPrependingRef: MutableRefObject<boolean>;
  activeScrollRef: RefObject<HTMLDivElement | null>;
  scrollRef: RefObject<HTMLDivElement | null>;
  topSentinelRef: RefObject<HTMLDivElement | null>;
  stickSessionToBottom: (behavior?: ScrollBehavior) => void;
};

/**
 * 会话窗口化的三件事，共用同一份 `visibleExchanges / visibleAux / windowMeta`：
 *   ① 切换会话时拉尾部窗口当首帧，并把可见行置底；
 *   ② 顶部哨兵进入视口就上翻一页（前插合并 + rAF 回补滚动位置）；
 *   ③ 首帧种子里只留**已落盘**的行。
 *
 * 从 SessionViewer 抽出（2026-10-08）。`applyWindow` 仍留在宿主 —— 方案 C 的重锚定
 * effect 也要用它，那条路径与窗口分页无关，搬进来会把两件事混在一起。
 */
export function useSessionWindowing(ctx: SessionWindowingCtx) {
  const {
    session,
    sessionKey,
    sessionNodeId,
    rootId,
    windowMeta,
    loadingMore,
    applyWindow,
    setVisibleExchanges,
    setVisibleAux,
    setWindowMeta,
    setLoadingMore,
    setShowJumpToLatest,
    shouldStickToBottomRef,
    isPrependingRef,
    activeScrollRef,
    scrollRef,
    topSentinelRef,
    stickSessionToBottom,
  } = ctx;

  // 初始化：会话切换时拉取尾部窗口，复位置底；拉取失败回退到 props 传入的全量/窗口。
  useEffect(() => {
    let cancelled = false;
    setLoadingMore(false);
    if (!sessionKey) {
      setVisibleExchanges([]);
      setVisibleAux({});
      setWindowMeta({ total: 0, hasMore: false, minSeq: 0, maxSeq: 0 });
      return;
    }
    // 进入窗口化视图，隔离 syncSession 的 truncated 全量回补（见 session.ts windowedView 标记）。
    setWindowedView(sessionKey, true);
    // 方案 B 首帧按需：避免先用**全量**做首帧导致长对话从头刷到尾。
    // 首帧种子是 App 缓存里已落盘的行（live 路径与 IndexedDB 都已按预算从**头部**
    // 淘汰过，见 toPersistentSession），这里不再二次截断 ——
    //
    // 曾经的 `slice(-SESSION_WINDOW_SIZE)` 就是「切会话闪一下」的成因（2026-10-05）：
    // 它在加载过程中把缓存的头部砍掉，只留尾部 20 条，随后服务端窗口再整体替换一次。
    // 用户看到的是「完整对话 → 塌成最后一条用户消息 → 回答慢慢补回来」。
    // 淘汰只能发生在**写入**时、只能从头淘汰，且不该在加载过程中再砍一遍。
    const incomingExs = Array.isArray(session?.exchanges) ? (session.exchanges as ExchangeArray) : ([] as ExchangeArray);
    const seedExs = incomingExs.filter((e) => isPersistedSeq((e as any)?.seq));
    const seedAux: Record<string, ExchangeAux[]> = {};
    const seedSeqs = new Set(seedExs.map((e) => Number((e as any)?.seq || 0)));
    for (const [k, v] of Object.entries((session?.exchange_aux || {}) as Record<string, ExchangeAux[]>)) {
      if (seedSeqs.has(Number(k))) seedAux[k] = v;
    }
    setVisibleExchanges(seedExs);
    setVisibleAux(seedAux);
    getSessionWindow(rootId || "", sessionKey, { latest: SESSION_WINDOW_SIZE, nodeId: sessionNodeId })
      .then((res) => {
        if (cancelled) return;
        // keepOlder：窗口只覆盖 [minSeq, maxSeq]，比 minSeq 更老的条目是首帧种子
        // （或 loadMore 翻出来的）——整体替换会把它们扔掉，表现为「刚加载出来的历史
        // 闪一下又没了」。合并后可见集单调增长，切换会话不再回缩。
        applyWindow(res, { keepOlder: true });
        if (res) {
          shouldStickToBottomRef.current = true;
          setShowJumpToLatest(false);
          window.requestAnimationFrame(() => stickSessionToBottom("auto"));
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
      clearWindowedView(sessionKey);
    };
  }, [sessionKey, rootId, applyWindow]);

  // 向上翻页：锚定 scrollHeight-scrollTop，请求 beforeSeq=minSeq 的历史段，
  // 函数式前置合并并 RAF 回补滚动位置，避免视口跳动。
  const loadMore = useCallback(() => {
    const container = activeScrollRef?.current;
    if (!container || loadingMore || !windowMeta.hasMore || !sessionKey) {
      return;
    }
    const anchor = container.scrollHeight - container.scrollTop;
    setLoadingMore(true);
    isPrependingRef.current = true;
    getSessionWindow(rootId || "", sessionKey, {
        beforeSeq: windowMeta.minSeq,
        limit: SESSION_WINDOW_SIZE,
        nodeId: sessionNodeId,
      })
      .then((res) => {
        if (!res) {
          setLoadingMore(false);
          return;
        }
        const winSession = res.session as any;
        const winExchanges = Array.isArray(winSession?.exchanges)
          ? (winSession.exchanges as ExchangeArray)
          : [];
        const winAux =
          (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
        setVisibleExchanges((prev) => mergeSessionExchanges(winExchanges, prev) as ExchangeArray);
        setVisibleAux((prev) => ({ ...prev, ...winAux }));
        setWindowMeta(res.meta);
        setLoadingMore(false);
        window.requestAnimationFrame(() => {
          const el = activeScrollRef?.current;
          if (el) {
            el.scrollTop = el.scrollHeight - anchor;
          }
          isPrependingRef.current = false;
        });
      })
      .catch(() => setLoadingMore(false));
  }, [loadingMore, windowMeta, sessionKey, rootId]);

  // 顶部哨兵：进入视口（rootMargin 200px）触发 loadMore，hasMore 耗尽后自动不观察。
  useEffect(() => {
    const sentinel = topSentinelRef.current;
    const root = scrollRef.current;
    if (!sentinel || !root || !windowMeta.hasMore) {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          loadMore();
        }
      },
      { root, rootMargin: "200px 0px 0px 0px", threshold: 0 },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [windowMeta.hasMore, loadMore]);
}
