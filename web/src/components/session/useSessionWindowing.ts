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
  type SessionBlock,
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
  setVisibleBlocks: Dispatch<SetStateAction<Record<string, SessionBlock[]>>>;
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
    setVisibleBlocks,
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
      setVisibleBlocks({});
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
    const seedBlocks: Record<string, SessionBlock[]> = {};
    for (const [k, v] of Object.entries(
      (session?.blocks || {}) as Record<string, SessionBlock[]>,
    )) {
      if (seedSeqs.has(Number(k))) seedBlocks[k] = v;
    }
    setVisibleExchanges(seedExs);
    setVisibleAux(seedAux);
    setVisibleBlocks(seedBlocks);
    // 窗口态按会话重建。留着上一个会话的 meta 会让 `applyWindow(keepOlder)` 取
    // `min(旧 minSeq, 新 minSeq)` —— 旧值更小时 `minSeq` 被永久钉小，`loadMore` 于是
    // 反复请求「比本会话所有历史都老」的一段，服务端回空、`res` 为 null 直接返回，
    // **上翻一页也加载不出来**（2026-10-27 由分页 fixture 暴露：从 seq 起于 1 的会话
    // 切到起于 233 的会话，`beforeSeq=1` 恒空）。
    setWindowMeta({ total: 0, hasMore: false, minSeq: 0, maxSeq: 0 });
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

  // 向上翻页：锚定视口内第一条可见行，请求 beforeSeq=minSeq 的历史段，
  // 函数式前置合并后在随后的若干帧里按**真实矩形**回补滚动位置。
  const loadMore = useCallback(() => {
    const container = activeScrollRef?.current;
    if (!container || loadingMore || !windowMeta.hasMore || !sessionKey) {
      return;
    }
    // G-KB 回归修复（2026-10-27）：以前只记 `scrollHeight - scrollTop` 并在一个 rAF 里
    // 写一次。虚拟化路径下这个 scrollHeight 是 estimateSize 撑出来的占位高度，
    // 前插后 overscan 那几行先挂载再实测，每行 estimate→actual 的差值都会让下面所有行
    // 整体位移；而 G-KD 的护栏在后退方向**刻意不补偿**（手势优先），位移就直接落在视口上，
    // 表现为「滚着滚着突然往上跳一大截」。
    // 现在锚定**元素身份 + 真实矩形**，连续多帧收敛：库补没补、补了多少都被自动吸收，
    // 不会双补偿。
    const containerTop = container.getBoundingClientRect().top;
    let anchorKey = "";
    let anchorTop = 0;
    const rows = container.querySelectorAll<HTMLElement>("[data-timeline-key]");
    for (const row of rows) {
      const top = row.getBoundingClientRect().top;
      if (top >= containerTop - 1) {
        anchorKey = row.dataset.timelineKey || "";
        anchorTop = top;
        break;
      }
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
          isPrependingRef.current = false;
          setLoadingMore(false);
          return;
        }
        const winSession = res.session as any;
        const winExchanges = Array.isArray(winSession?.exchanges)
          ? (winSession.exchanges as ExchangeArray)
          : [];
        const winAux =
          (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
        const winBlocks =
          (winSession?.blocks as Record<string, SessionBlock[]>) || {};
        setVisibleExchanges((prev) => mergeSessionExchanges(winExchanges, prev) as ExchangeArray);
        setVisibleAux((prev) => ({ ...prev, ...winAux }));
        setVisibleBlocks((prev) => ({ ...prev, ...winBlocks }));
        setWindowMeta(res.meta);
        setLoadingMore(false);
        let frames = 0;
        let stableFrames = 0;
        // 帧数上限按「稳定即收工」定：新挂载行的实测（ResizeObserver）与 loading 占位的
        // 消失都可能在若干帧之后才落地，固定跑 N 帧会在测量到达前收工，留下十几像素的残差。
        const MAX_FRAMES = 40;
        const MIN_FRAMES = 4;
        const SETTLE_PX = 1;
        const settle = () => {
          const el = activeScrollRef?.current;
          if (!el) {
            isPrependingRef.current = false;
            return;
          }
          if (anchorKey) {
            const row = el.querySelector<HTMLElement>(
              `[data-timeline-key="${CSS.escape(anchorKey)}"]`,
            );
            if (row) {
              const delta = row.getBoundingClientRect().top - anchorTop;
              if (Math.abs(delta) > SETTLE_PX) {
                el.scrollTop += delta;
                anchorTop = row.getBoundingClientRect().top;
                stableFrames = 0;
              } else {
                stableFrames += 1;
              }
              frames += 1;
              if ((frames < MIN_FRAMES || stableFrames < 3) && frames < MAX_FRAMES) {
                window.requestAnimationFrame(settle);
                return;
              }
            } else if (frames < 2) {
              frames += 1;
              window.requestAnimationFrame(settle);
              return;
            }
          }
          // 非虚拟化路径（没有锚点行）：退回按 scrollHeight 差值回补。
          if (!anchorKey) {
            el.scrollTop = el.scrollHeight - anchor;
          }
          isPrependingRef.current = false;
        };
        window.requestAnimationFrame(settle);
      })
      .catch(() => {
        isPrependingRef.current = false;
        setLoadingMore(false);
      });
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
