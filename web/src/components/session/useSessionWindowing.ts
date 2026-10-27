import { useCallback, useEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
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

/**
 * 顶部预取的提前量：哨兵距视口顶不足「视口高度 × 这个比例」就请求上一页。
 *
 * 原先只提前 200px，用户得**先看见内容不够**才等到加载，观感上是「滚到顶停一下、然后
 * 突然补一屏」。半屏是手机与桌面都够用的提前量：一页通常不止半屏，所以一次手势仍然
 * 只翻一页（见 `armedRef`）。
 */
const LOAD_MORE_AHEAD_RATIO = 0.5;

/** 前插后要再往上滚这么多才算「用户又动了」，才允许下一次自动加载。 */
const PREPEND_REARM_PX = 24;

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

  // 「允许自动加载」的闸门。每次前插完成后置假，用户再往上滚 PREPEND_REARM_PX 才置真。
  // 没有它，触发点提前半屏之后，回补完成时哨兵往往仍在提前量之内，IO 重建即回调，
  // 一次手势能连翻好几页（2026-10-27 已因同样形态连翻 29 页，见 loadMore 注释）。
  const armedRef = useRef(true);
  // 重新武装必须**重建 observer**：哨兵在回补后一直停在提前量之内，intersection 状态
  // 没有变化，IO 不会再投递一次 —— 只把 ref 置真，下一页永远等不到触发（实测：置于 259px
  // 处再滚也不加载）。epoch 变化让下方 effect 重跑，新 observer 立刻投递一次 intersecting。
  const [armEpoch, setArmEpoch] = useState(0);
  // 用 ref 记住上一次 scrollTop 与当前会话，避免给 effect 增加依赖（哨兵 effect 每换一次
  // loadMore 身份就要重建 observer，多一个依赖就多一次重建）。
  const armWatchRef = useRef<{ key: string | null; top: number }>({ key: null, top: 0 });

  // 初始化：会话切换时拉取尾部窗口，复位置底；拉取失败回退到 props 传入的全量/窗口。
  useEffect(() => {
    let cancelled = false;
    setLoadingMore(false);
    // 切会话重置闸门：新会话的首屏还没铺满，哨兵本来就在视口里，这时必须允许加载。
    armedRef.current = true;
    armWatchRef.current = { key: sessionKey, top: 0 };
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
  // 函数式前置合并后在若干帧里按**真实矩形**回补滚动位置。
  const loadMore = useCallback(() => {
    const container = activeScrollRef?.current;
    // isPrependingRef：上一页还在回补位置时不接新请求。否则哨兵（scrollTop 尚未恢复时
    // 仍停在视口里）会把下一页连着点着，一次上滑把历史全部加载完（2026-10-27 真机）。
    // armedRef：每次前插之后必须**再滚一次**才重新武装。触发点提前到半屏以后，哨兵在
    // 回补完成后可能仍落在提前量之内（短页更是如此），IO 每次重建都会立刻回调 ——
    // 只靠 isPrependingRef 挡不住「page 之间的间隙」，一次手势又会连翻好几页。
    if (
      !container ||
      loadingMore ||
      isPrependingRef.current ||
      !armedRef.current ||
      !windowMeta.hasMore ||
      !sessionKey
    ) {
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
        // 状态提交必须同步落地（flushSync），否则「提交」和「回补」会跨帧错位：
        // 平时 React 会把这四个 setState 排到下一帧，而 rAF 回调完全可能插在提交**之前**
        // 跑 —— 那一帧读到的还是旧 DOM，delta 为 0、回补落空；新行则在下一帧一次性推上来，
        // 用户看到的就是「闪一下 + 位置抖一下」（2026-10-27 真机）。
        // flushSync 让新行在本次任务内进入 DOM，紧接着的一次同步回补就发生在**浏览器绘制
        // 之前**，中间没有任何一帧呈现未补偿的状态。
        flushSync(() => {
          setVisibleExchanges((prev) => mergeSessionExchanges(winExchanges, prev) as ExchangeArray);
          setVisibleAux((prev) => ({ ...prev, ...winAux }));
          setVisibleBlocks((prev) => ({ ...prev, ...winBlocks }));
          setWindowMeta(res.meta);
          setLoadingMore(false);
        });
        let frames = 0;
        let stableFrames = 0;
        // 帧数上限按「稳定即收工」定：新挂载行的实测（ResizeObserver）与 loading 占位的
        // 消失都可能在若干帧之后才落地，固定跑 N 帧会在测量到达前收工，留下十几像素的残差。
        const MAX_FRAMES = 40;
        const MIN_FRAMES = 4;
        const SETTLE_PX = 1;
        // 单步回补：把视口拉回锚行原处。返回 false 表示容器已不在。
        const settleStep = () => {
          const el = activeScrollRef?.current;
          if (!el) return false;
          const row = anchorKey
            ? el.querySelector<HTMLElement>(`[data-timeline-key="${CSS.escape(anchorKey)}"]`)
            : null;
          // 锚行在 → 按真实矩形回补；锚行没挂载 → 按 scrollHeight 差值回补。
          //
          // 后者是必须的兜底，不是可选路径（2026-10-27 真机）：真实会话一页 8 条
          // exchange 能展开成 70+ 个时间线条目，前插后原来那行被推到索引 40 开外，
          // 远超视口 + overscan，于是**永远不会再挂载**。此时若不兜底，两条补偿都不走、
          // 一个字节都不写，scrollTop 停在 0 → 顶部哨兵持续 intersecting → 每建一次
          // observer 就点着下一轮 loadMore，一次上滑把整段历史连翻到底（实测 29 次请求
          // 冲完 240 条、停在顶部）。小 fixture 每页只有 16 条、前后都还在挂载，所以
          // 这条路径从来没被测到。
          const delta = row
            ? row.getBoundingClientRect().top - anchorTop
            : el.scrollHeight - anchor - el.scrollTop;
          if (Math.abs(delta) > SETTLE_PX) {
            el.scrollTop += delta;
            if (row) anchorTop = row.getBoundingClientRect().top;
            stableFrames = 0;
          } else {
            stableFrames += 1;
          }
          frames += 1;
          return true;
        };
        // 第一次同步跑（绘制前），之后交回 rAF 继续吸收测量修正。
        if (!settleStep()) {
          isPrependingRef.current = false;
          return;
        }
        const settle = () => {
          if (!settleStep()) {
            isPrependingRef.current = false;
            return;
          }
          if ((frames < MIN_FRAMES || stableFrames < 3) && frames < MAX_FRAMES) {
            window.requestAnimationFrame(settle);
            return;
          }
          isPrependingRef.current = false;
          // 收工即上闸：回补后视口停在这里，用户得再往上滚 PREPEND_REARM_PX 才允许下一页。
          armedRef.current = false;
          armWatchRef.current = { key: sessionKey, top: activeScrollRef?.current?.scrollTop ?? 0 };
        };
        window.requestAnimationFrame(settle);
      })
      .catch(() => {
        isPrependingRef.current = false;
        setLoadingMore(false);
      });
  }, [loadingMore, windowMeta, sessionKey, rootId]);

  // 顶部哨兵：进入视口（提前量为半个视口）触发 loadMore，hasMore 耗尽后自动不观察。
  //
  // 提前量从 200px 提到半屏：原先要「看见内容不够」才开始加载，观感是滚到顶停一下再补一屏。
  // 提前之后，哨兵在回补完成后通常仍落在提前量之内，所以必须配 `armedRef` —— 每次前插
  // 完成后置假，只有用户再往上滚 PREPEND_REARM_PX 才重新武装，否则 IO 每次重建都会立刻回调。
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
      { root, rootMargin: `${Math.round((root.clientHeight || 0) * LOAD_MORE_AHEAD_RATIO)}px 0px 0px 0px`, threshold: 0 },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [windowMeta.hasMore, loadMore, armEpoch]);

  // 重新武装：前插落地后记下视口位置，用户再往上滚 PREPEND_REARM_PX 才允许下一次自动加载。
  // 单独一个 effect（依赖只有 sessionKey），避免给哨兵 effect 增加依赖 —— 它每换一次
  // loadMore 身份就要重建 observer，多一个依赖就多一次重建。
  useEffect(() => {
    const root = scrollRef.current;
    if (!root) return;
    const onScroll = () => {
      if (armedRef.current) return;
      const anchor = armWatchRef.current;
      if (anchor.key !== sessionKey) return;
      if (root.scrollTop < anchor.top - PREPEND_REARM_PX) {
        armedRef.current = true;
        // 重建 observer：上一页回补时它已经在提前量之内 intersecting 过一次，
        // 之后没有状态变化就不会再投递 —— 只置 ref 的话，下一页永远等不到触发。
        setArmEpoch((n) => n + 1);
      }
    };
    root.addEventListener("scroll", onScroll, { passive: true });
    return () => root.removeEventListener("scroll", onScroll);
  }, [sessionKey]);
}
