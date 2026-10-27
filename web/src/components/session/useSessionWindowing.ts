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
 * 原先只提前 200px，用户得**先看见内容不够**才等到加载，观感上是「滚到顶停一下、
 * 然后突然补一屏」。现在提前整整一屏：滚到离顶还剩一屏时就该把下一页拿回来，
 * 用户基本不会滚到「没有内容」的位置。
 *
 * 提前量必须按**真实视口高度**算，不能写死像素 —— 手机与桌面的可视高度差一倍以上，
 * 写死值必然在其中一端要么太晚要么刚进页面就触发。这一屏也是 `armedRef` 的容量依据：
 * 一页通常不止一屏，所以一次手势仍然只翻一页。
 */
const LOAD_MORE_AHEAD_RATIO = 1;

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
  // 没有它，触发点提前一屏之后，回补完成时哨兵往往仍在提前量之内，IO 重建即回调，
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
    // armedRef：每次前插之后必须**再滚一次**才重新武装。触发点提前到一屏以后，哨兵在
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
    // 现在锚定**元素身份 + 屏幕 y**，连续多帧收敛：库补没补、补了多少都被自动吸收，
    // 不会双补偿。
    //
    // 锚点必须在**回包那一刻**采样，不能在发请求时采样（2026-10-28）。
    //
    // 请求飞行几百毫秒，这期间用户往往还在上滚 —— 提前量是一整屏，请求本来就是在他滚的
    // 时候发出去的。若拿发请求时的锚点当参照系，`settleStep` 量到的位移里就混进了他自己
    // 滚掉的那一段 S，于是把 S 一起补偿掉，写完视口整体跳 S：用户看到的「加载完成后还是
    // 闪一下」。真机实测：15 行同帧整齐上移 480px（= 4 个滚轮格，正好是飞行期间的上滚量）。
    // 换成回包那一刻的参照系后，需要补偿的就只剩「新插入内容的高度」。
    let anchorKey = "";
    // 参照系用**屏幕 y**，不是内容坐标。这是踩过两次才定下来的：
    //
    //  - 内容坐标（`rect.top - 容器顶 + scrollTop`）看着更「干净」，但虚拟器自己在前进方向
    //    会写一笔 scrollTop 补偿（`scrollWithAdjustments`，正是为了让行在屏幕上不动），
    //    内容坐标把这一笔又抵消掉 ⇒ 差额仍是全额 ⇒ 我们再补一次。夹具实测：库已写
    //    4480→4698，我们又写 +218，视口整体多滚 218px。屏幕 y 天然吸收库的补偿。
    //  - 屏幕 y 唯一分不开的是**用户自己滚了**，而那有方向：用户上滚只会让行**往上**走
    //    （delta 为负），前插/测量只会让行**往下**走（delta 为正）。见 settleStep。
    //
    // 不能用 offsetTop：虚拟行是绝对定位 + `transform: translateY(...)`，offsetTop 恒 0。
    let anchorTop = 0;
    let anchor = 0;
    const captureAnchor = () => {
      const el = activeScrollRef?.current;
      if (!el) return;
      const boxTop = el.getBoundingClientRect().top;
      for (const row of el.querySelectorAll<HTMLElement>("[data-timeline-key]")) {
        const top = row.getBoundingClientRect().top;
        if (top >= boxTop - 1) {
          anchorKey = row.dataset.timelineKey || "";
          anchorTop = top;
          break;
        }
      }
      anchor = el.scrollHeight - el.scrollTop;
    };
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
        // 锚点在这一刻才取 —— 见 loadMore 开头 `captureAnchor` 的注释。
        captureAnchor();
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
        // 收工判据是「连续 3 帧不动」，但新挂载行的测量是**分批**到达的：两批之间可能隔着
        // 好几个安静帧，循环就此收工，下一批实测把视口上方的高度又改了 —— 那时没人再补，
        // 残差直接落在屏幕上（2026-10-28 实测：3 次里有 1 次在收工后 `scrollHeight` 又涨了
        // 699px，`scrollTop` 没跟上，视口内的行整体下移 120px = 一个滚轮格）。
        // 所以「内容高度还在变」也算不稳定，必须继续补到它不动为止。
        let lastHeight = activeScrollRef?.current?.scrollHeight ?? 0;
        // 上一帧收尾时的 scrollTop。用来认出「这一帧的位移是用户滚的」：库只会往**大**了写
        // （`shouldAdjustScrollPositionOnItemSizeChange` 在后退方向被 G-KD 整个关掉），
        // 所以 scrollTop 变小只可能来自手势。
        let lastScrollTop = activeScrollRef?.current?.scrollTop ?? 0;
        // 单步回补：把锚行放回它**在屏幕上的原处**。返回 false 表示容器已不在。
        const settleStep = () => {
          const el = activeScrollRef?.current;
          if (!el) return false;
          if (el.scrollHeight !== lastHeight) {
            lastHeight = el.scrollHeight;
            stableFrames = 0;
          }
          const row = anchorKey
            ? el.querySelector<HTMLElement>(`[data-timeline-key="${CSS.escape(anchorKey)}"]`)
            : null;
          // 用户自己在滚 → **让位**：只把参照系挪到它现在的位置，一个字节都不写。
          // 屏幕 y 分不开「内容变了」和「用户滚了」，但方向分得开（见上）。不回补的话，
          // 他滚多少就被原样滚多少 —— 这正是要的；回补则是把他滚掉的距离又补回去，
          // 屏幕上整块上行一个滚轮格（2026-10-28 实测 3/5 次）。
          if (el.scrollTop - lastScrollTop < -SETTLE_PX) {
            if (row) anchorTop = row.getBoundingClientRect().top;
            anchor = el.scrollHeight - el.scrollTop;
            lastScrollTop = el.scrollTop;
            stableFrames += 1;
            frames += 1;
            return true;
          }
          // 锚行在 → 按它的屏幕 y 回补；锚行没挂载 → 按内容总高度回补。
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
            // 补完必须**推进参照系**：不推进的话下一帧会量到同一笔差额再补一遍
            // （内容坐标那版实测 +4480 连写两次，视口多滚 218px）。
            if (row) anchorTop = row.getBoundingClientRect().top;
            else anchor = el.scrollHeight - el.scrollTop;
            stableFrames = 0;
          } else {
            stableFrames += 1;
          }
          lastScrollTop = el.scrollTop;
          frames += 1;
          return true;
        };
        // 第一次同步跑（绘制前），之后交回 rAF 继续吸收测量修正。
        if (!settleStep()) {
          isPrependingRef.current = false;
          return;
        }
        // 让虚拟器**在本次任务内**按新的 scrollTop 重算可见区间（2026-10-27 全屏闪烁）。
        //
        // 上面的 flushSync 提交时，时间线已经是「新页 + 旧页」，但虚拟器的 offset 还停在
        // 前插前的 scrollTop —— 它只从 scroll 事件更新，而 scroll 事件要等浏览器渲染步骤才
        // 派发。于是这次提交按旧 offset 去 map 新数组：原来视口那一段被推到很后面，落进
        // 视口区间的是**刚插进来的最老一段**（实测 p-177-*，33 行全在视口上方，视口覆盖率
        // 0%）。等 scroll 事件到了才换成 p-185-*，那一帧空白就此被画出去 —— 用户看到的
        // 「加载完全屏闪一下」。
        // 同步派发一次 scroll 让库自己走它设计好的入口（读 offset → 重算 range → flushSync
        // 重渲染），第二次提交仍在绘制前完成，中间态一次都不呈现。
        container.dispatchEvent(new Event("scroll"));
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

  // 顶部哨兵：进入视口（提前量为一整个视口）触发 loadMore，hasMore 耗尽后自动不观察。
  //
  // 提前量从 200px → 半屏 → 一屏：原先要「看见内容不够」才开始加载，观感是滚到顶停一下再补一屏。
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
