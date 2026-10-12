import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSessionStream, type TimelineItem } from "../../hooks/useSessionStream";
import { useTargetSeqScroll } from "./useTargetSeqScroll";
import type { TodoUpdate } from "../../services/session";
import { ViewHeader } from "../../layout/ViewHeader";
// 窗口取数（SESSION_WINDOW_SIZE / getSessionWindow / setWindowedView /
// clearWindowedView / mergeSessionExchanges 的另一半）已随 2026-10-08 的拆分
// 搬进 ./useSessionWindowing，这里只剩仍被重锚定与时间线用到的这几个。
import { isPersistedSeq, mergeSessionExchanges, type ExchangeAux, type SessionBlock, type SessionWindowMeta } from "../../services/session";
import { savePrompt } from "../../services/prompts";
import { reportError } from "../../services/net/error";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { copyText } from "../../services/platform/clipboard";
import type { AgentStatus } from "../../services/agents";
import { useI18n } from "../../i18n/index";
import { formatSessionDuration } from "../../services/sessionDuration";
import {
  computeTailOverlay,
  type OverlayExchange,
} from "../../shared/sessionOverlay";
import {
  relatedFileStatKey,
  useRelatedFileStats,
} from "../../hooks/useRelatedFileStats";

import {
  ContextWindowBadge,
  TurnTokenUsage,
} from "./SessionViewerTokenViews";
import {
} from "./SessionViewerCards";
import {
  extractUploadAttachments,
  formatAssistantExchangeMeta,
  formatTime,
  formatToolCallFallbackResult,
  normalizeUserMessageSummary,
  shouldDefaultCollapseRelatedFiles,
  stripImageAttachmentTokens,
  stripUploadAttachmentTokens,
} from "./sessionViewerFormat";
import type { ExchangeArray, SessionItem, SessionViewerProps } from "./sessionViewerTypes";
import { useSessionWindowing } from "./useSessionWindowing";
import { SessionViewerSlashCommandResult } from "./SessionViewerSlashCommand";
import { SessionViewerRelatedFiles } from "./SessionViewerRelatedFiles";
import { SessionViewerHeader } from "./SessionViewerHeader";
import { SessionViewerLoadingState } from "./SessionViewerLoadingState";
import { SessionViewerWaitingIndicator } from "./SessionViewerWaitingIndicator";
import { SessionViewerFloatingControls } from "./SessionViewerFloatingControls";
import { buildUserMetaButtonStyle } from "./sessionViewerStyles";
import { SessionViewerTimeline } from "./SessionViewerTimeline";
import { useUserMessageSummaries } from "./useUserMessageSummaries";
import { useRelatedFiles } from "./useRelatedFiles";

// CUSTOM(G-AT): tool_group 由时间线渲染器统一处理，保持 fork 的分组显示语义。

function SessionViewerInner({
  session,
  loading = false,
  slashCommandResult = null,
  rootId,
  rootDisplayName,
  rootPath,
  rootColor,
  interactionMode = "main",
  targetSeq = 0,
  targetSeqRequestKey = "",
  gitFileStatsByPath = {},
  onFileClick,
  onRootClick,
  onRemoveRelatedFile,
  onAskUserAnswer,
  onEditUserMessage,
  onForkAgentMessage,
  agents,
  composerOverlayInset = 0,
  scrollContainerRef,
}: SessionViewerProps) {
  const { locale, t } = useI18n();
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.matchMedia("(max-width: 767px)").matches;
  });
  const [savedPromptKeys, setSavedPromptKeys] = useState<Record<string, true>>(
    {},
  );
  const [copiedMessageKeys, setCopiedMessageKeys] = useState<
    Record<string, true>
  >({});
  const scrollEndRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  // ponytail: drawer now uses inner scroll container (upstream 6247756: BottomSheet overflow:hidden, inner overflow:auto)
  // outer drawerScrollRef is kept for BottomSheet sizing only, not for stick-to-bottom.
  const activeScrollRef = scrollRef;
  const onFileClickRef = useRef(onFileClick);
  const copyResetTimersRef = useRef<Record<string, number>>({});
  const sessionKey = session?.key || session?.session_key || null;
  // 会话归属节点：窗口拉取/图片等请求须路由到会话所在节点。同名根（如两台机器都有
  // root "mindfs"）仅凭裸 rootId 查裸键映射会串到另一节点（实测窗口 GET 404）。
  const sessionNodeId =
    String((session as any)?._nodeId || "").trim() || undefined;
  // 方案 B（超长会话窗口化）：可视数据以可见窗口为准，初始用全量/窗口做首帧，
  // 随后由 getSessionWindow({ latest: SESSION_WINDOW_SIZE }) 覆盖（见下方初始化 effect）。
  const [visibleExchanges, setVisibleExchanges] = useState<ExchangeArray>(
    () => (Array.isArray(session?.exchanges) ? session.exchanges : []),
  );
  const [visibleAux, setVisibleAux] = useState<Record<string, ExchangeAux[]>>(
    () => session?.exchange_aux || {},
  );
  // 正文块（CUSTOM(G-BH)，方案 §7）。与 visibleAux 同生命周期、同合并语义：
  // 它是正文的**唯一**载体，`content` 只是块流缺失时的回退。
  const [visibleBlocks, setVisibleBlocks] = useState<Record<string, SessionBlock[]>>(
    () => (session?.blocks as Record<string, SessionBlock[]>) || {},
  );
  const [windowMeta, setWindowMeta] = useState<SessionWindowMeta>({
    total: 0,
    hasMore: false,
    minSeq: 0,
    maxSeq: 0,
  });
  const [loadingMore, setLoadingMore] = useState(false);
  const topSentinelRef = useRef<HTMLDivElement>(null);
  const isPrependingRef = useRef(false);
  const isAwaiting = !!(session as any)?.pending;
  // 方案 C：overlay 尾巴 = App 缓存里「窗口尚未覆盖」的条目，只读派生、永不合并回窗口。
  // 窗口是唯一持久化源，只在 init 拉取 / 翻页前插 / 重锚定（_windowMeta）时整体替换。
  //
  // 判定刻意不依赖 windowMeta：loadMore 与 targetSeq 取窗都会用旧/中段窗口的 meta 覆盖
  // windowMeta，拿它当「会话最新 seq」会塌回旧值（曾导致按位次裁剪错算并把实时流式内容
  // 一起 slice 掉）。改为三个规则：
  //   - seq>0：由 latestSeq 界定。latestSeq 只增不减、按会话键绑定，且仅在 init/anchor 这类
  //     「最新窗口」经 applyWindow 更新（loadMore / targetSeq 不碰）。seq<=latestSeq → 窗口已含
  //     或即将含，丢弃；seq>latestSeq → 刚发出、窗口还没追上，保留（自己发的消息立即出现）。
  //   - seq==0 且 role=user：按 content 与窗口计数消抵——窗口里同内容出现 covered 次，就丢弃
  //     缓存中同内容的前 covered 个，其余保留。这是跨端丢事件（手机后台 / WS 断连）时的兜底，
  //     不依赖时间戳（客户端 ISO 毫秒 vs 服务端 RFC3339Nano 永不相等）。
  //   - seq==0 且 role=tool：callId 已出现在窗口 aux → 让位（见 windowToolCallIds）。
  //   - seq==0 的其它（流式文本 / 思考）：默认保留（流式内容要实时可见），但若窗口最新几条
  //     持久化行里已经包含它（去空白后判定），说明这一轮已落盘、这份是陈旧拷贝 → 让位。
  //     这是 2026-09-17 的缺口：以前这里「一律保留」，于是 ask 前后被渲染成两块一样的正文。
  // 只读派生不写回缓存，避免覆盖其它标签页可能正在流式写入的持久化缓存。
  // 瞬时缓存与窗口只按 MindFS 身份对账；文本相似不能证明两行相同。
  // 持久行按 seq；无身份的瞬时行继续保留，确保实时正文与思考不会被丢。
  const tailOverlay = useMemo(
    () =>
      computeTailOverlay({
        exchanges: session?.exchanges as OverlayExchange[] | undefined,
        visibleExchanges: visibleExchanges as OverlayExchange[],
      }),
    [session?.exchanges, visibleExchanges],
  );
  // 窗口态与 overlay 是两条独立来源，同一个 exchange 对象可能两边都在（重锚定会把整份
  // 缓存装进窗口态时就发生过）。按对象同一性取差集，保证同一份 exchange 只渲染一次 ——
  // 与 dedupeToolCards 同一条约定，只是这里对所有类型生效（thought/正文没有 callId 可去重）。
  const composedExchanges = useMemo(() => {
    const inWindow = new Set(visibleExchanges as unknown[]);
    const extra = tailOverlay.filter((ex) => !inWindow.has(ex));
    return [...visibleExchanges, ...extra] as ExchangeArray;
  }, [visibleExchanges, tailOverlay]);
  const { timeline, isStreaming, streamVersion, streamStatusText } = useSessionStream(
    sessionKey,
    composedExchanges,
    visibleAux,
    session?.context_window,
    isAwaiting,
    visibleBlocks,
  );
  // CUSTOM(G-AU): 原先在 timeline.map 里对每个 user 项做「切前缀再 filter 数 user」、
  // 对每个 assistant 项做 previousUserTimestamp 反向扫描 —— 两处都是 O(n²)，
  // 且每轮渲染都重算（流式输出 33 次/秒）。改成一次 O(n) 前向扫描，渲染里只查表。
  const timelineItemMeta = useMemo(() => {
    const metas = new Array<{
      userIndex: number;
      prevUserTs: string | undefined;
    }>(timeline.length);
    let userCount = 0;
    let lastUserTs: string | undefined;
    for (let i = 0; i < timeline.length; i += 1) {
      const entry = timeline[i] as { type?: string; timestamp?: number | string } | undefined;
      const isUser = entry?.type === "user_text";
      if (isUser) {
        userCount += 1;
        lastUserTs = entry?.timestamp ? String(entry.timestamp) : undefined;
      }
      metas[i] = {
        userIndex: isUser ? userCount : 0,
        prevUserTs: isUser ? undefined : lastUserTs,
      };
    }
    return metas;
  }, [timeline]);
  const shouldStickToBottomRef = useRef(true);
  const lastSessionKeyRef = useRef<string | null>(null);
  const targetSeqScrollKeyRef = useRef("");
  const targetSeqFrameRef = useRef<number | null>(null);
  const targetSeqTimerRefs = useRef<number[]>([]);
  const [showJumpToLatest, setShowJumpToLatest] = useState(false);
  const viewportStickFrameRef = useRef<number | null>(null);

  const cancelTargetSeqScroll = () => {
    if (targetSeqFrameRef.current !== null) {
      window.cancelAnimationFrame(targetSeqFrameRef.current);
      targetSeqFrameRef.current = null;
    }
    targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    targetSeqTimerRefs.current = [];
  };

  const stickSessionToBottom = (behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    container.scrollTo({ top: maxTop, behavior });
  };

  useEffect(() => {
    onFileClickRef.current = onFileClick;
  }, [onFileClick]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const media = window.matchMedia("(max-width: 767px)");
    const handleChange = () => {
      setIsMobile(media.matches);
    };
    handleChange();
    media.addEventListener("change", handleChange);
    return () => {
      media.removeEventListener("change", handleChange);
    };
  }, []);

  useEffect(() => {
    setSavedPromptKeys({});
    setCopiedMessageKeys({});
    Object.values(copyResetTimersRef.current).forEach((timer) =>
      window.clearTimeout(timer),
    );
    copyResetTimersRef.current = {};
  }, [sessionKey]);

  // 方案 B：从窗口响应中提取 exchanges/aux/meta（后端 window_meta 透传 total/hasMore/minSeq/maxSeq）。
  const applyWindow = useCallback(
    (
      res: { session: any; meta: SessionWindowMeta } | null,
      opts?: { keepOlder?: boolean },
    ) => {
      if (!res) return false;
      const winSession = res.session as any;
      const winExchanges = Array.isArray(winSession?.exchanges)
        ? (winSession.exchanges as ExchangeArray)
        : [];
      const winAux =
        (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
      const winBlocks =
        (winSession?.blocks as Record<string, SessionBlock[]>) || {};
      setVisibleExchanges((prev) => {
        if (!opts?.keepOlder) return winExchanges;
        // keepOlder：窗口只覆盖 [minSeq, maxSeq]，比 minSeq 更老的首帧种子 / loadMore
        // 翻出来的历史必须保住 —— 整体替换会把它们扔掉，表现为「刚加载出来的历史闪一下
        // 又没了」，顶部哨兵再次可见又触发 loadMore，形成 40↔20 的无限抖动。
        return mergeSessionExchanges(prev, winExchanges) as ExchangeArray;
      });
      setVisibleAux((prev) =>
        opts?.keepOlder ? { ...prev, ...winAux } : winAux,
      );
      // 块与 aux 同一合并语义：keepOlder 时窗口只覆盖 [minSeq, maxSeq]，
      // 整体替换会把种子/翻出来的历史那部分块扔掉（正文闪一下又没了）。
      setVisibleBlocks((prev) =>
        opts?.keepOlder ? { ...prev, ...winBlocks } : winBlocks,
      );
      // minSeq 必须报**实际可见的最小 seq**，不能直接用窗口那份：keepOlder 合并进来的
      // 种子可能比窗口更老，用窗口的 minSeq 会让下一次 loadMore 去取已经有的行
      // （渲染两遍），窗口越新、种子越全越容易触发。
      setWindowMeta((prevMeta) => {
        const winMinSeq = Number(res.meta?.minSeq || 0);
        if (!opts?.keepOlder || !winMinSeq) return res.meta;
        return {
          ...res.meta,
          minSeq: Math.min(winMinSeq, Number(prevMeta?.minSeq || 0) || winMinSeq),
        };
      });
      return true;
    },
    [],
  );

  useSessionWindowing({
    session, sessionKey, sessionNodeId, rootId, windowMeta, loadingMore, applyWindow,
    setVisibleExchanges, setVisibleAux, setVisibleBlocks, setWindowMeta, setLoadingMore, setShowJumpToLatest,
    shouldStickToBottomRef, isPrependingRef, activeScrollRef, scrollRef, topSentinelRef,
    stickSessionToBottom,
  });
  // 方案 C 重锚定（done/重连）：App 侧 restoreActiveSession 替换缓存并附 _windowMeta/_anchoredAt；
  // 此处按锚点一次性原子换窗——窗口整体替换，overlay 尾巴自然清空（缓存尾巴已被 App 清掉）。
  // 旧锚点不重复应用（App 的 spread 会把 _windowMeta 带到后续 cache 对象上）。
  const lastAppliedAnchorRef = useRef<{ key: string | null; at: number }>({
    key: null,
    at: -1,
  });
  useEffect(() => {
    const anchorAt = Number((session as any)?._anchoredAt || 0);
    const anchorMeta = (session as any)?._windowMeta as
      | SessionWindowMeta
      | undefined;
    if (!anchorAt || !anchorMeta || !sessionKey) {
      return;
    }
    const last = lastAppliedAnchorRef.current;
    if (last.key === sessionKey && last.at >= anchorAt) {
      return;
    }
    lastAppliedAnchorRef.current = { key: sessionKey, at: anchorAt };
    // 锚点载荷是 App 的整份缓存：持久化行 + 直播瞬时行（seq=0，见 App.loadSession 的
    // [...incoming.filter(seq>0), ...localTransient]）。窗口态只能装持久化行 ——
    // 瞬时行归 overlay 管，混进窗口后 overlay 会再输出一遍，同一段正文渲染两次
    // （实测 2026-09-17：ask 触发后 ask 上下各一块同样的分析文本，重开会话必现）。
    const anchorExchanges = (Array.isArray((session as any)?.exchanges)
      ? ((session as any).exchanges as ExchangeArray)
      : ([] as ExchangeArray)
    ).filter((ex) => isPersistedSeq((ex as any)?.seq));
    applyWindow(
      {
        session: { ...(session as any), exchanges: anchorExchanges },
        meta: anchorMeta,
      },
      { keepOlder: true },
    );
  }, [session, sessionKey, applyWindow]);

  useEffect(() => {
    return () => {
      Object.values(copyResetTimersRef.current).forEach((timer) =>
        window.clearTimeout(timer),
      );
      copyResetTimersRef.current = {};
      if (targetSeqFrameRef.current !== null) {
        window.cancelAnimationFrame(targetSeqFrameRef.current);
        targetSeqFrameRef.current = null;
      }
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
        viewportStickFrameRef.current = null;
      }
      targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
      targetSeqTimerRefs.current = [];
    };
  }, []);

  useEffect(() => {
    if (isPrependingRef.current) {
      // 顶部插入（loadMore 历史段）不触发底部跟随，视口由 loadMore 的 rAF 回补。
      return;
    }
    const container = activeScrollRef?.current;
    if (!container) {
      if (interactionMode === "drawer" && shouldStickToBottomRef.current) {
        const frame = window.requestAnimationFrame(() => {
          const retry = activeScrollRef?.current;
          if (retry && shouldStickToBottomRef.current) scrollToRelatedFilesDivider("auto");
        });
        return () => window.cancelAnimationFrame(frame);
      }
      return;
    }
    if (!scrollEndRef.current) {
      return;
    }
    const nextKey = sessionKey;
    const isSessionChanged = lastSessionKeyRef.current !== nextKey;
    if (isSessionChanged) {
      lastSessionKeyRef.current = nextKey;
      shouldStickToBottomRef.current = true;
    }
    if (shouldStickToBottomRef.current) {
      scrollToRelatedFilesDivider("auto");
    }
  }, [sessionKey, timeline, isStreaming, streamVersion, slashCommandResult]);

  useEffect(() => {
    const container = activeScrollRef?.current;
    if (!container || typeof window === "undefined") {
      return;
    }
    const queueStickToBottom = () => {
      if (!shouldStickToBottomRef.current) {
        return;
      }
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
      }
      viewportStickFrameRef.current = window.requestAnimationFrame(() => {
        viewportStickFrameRef.current = null;
        if (!shouldStickToBottomRef.current) {
          return;
        }
        stickSessionToBottom("auto");
      });
    };
    window.visualViewport?.addEventListener("resize", queueStickToBottom);
    window.visualViewport?.addEventListener("scroll", queueStickToBottom);
    window.addEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
    return () => {
      window.visualViewport?.removeEventListener("resize", queueStickToBottom);
      window.visualViewport?.removeEventListener("scroll", queueStickToBottom);
      window.removeEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
        viewportStickFrameRef.current = null;
      }
    };
  }, [sessionKey]);

  const {
    userMessageSummaries,
    userSummaryOpen,
    userSummaryRootRef,
    userSummaryListRef,
    currentUserMessageIndex,
    refreshCurrentUserMessageIndex,
    scrollToUserMessageSummary,
    setUserSummaryHoverOpen,
    setUserSummaryPinnedOpen,
  } = useUserMessageSummaries({
    timeline,
    t,
    sessionKey,
    activeScrollRef,
    shouldStickToBottomRef,
    cancelTargetSeqScroll,
  });

  useEffect(() => {
    const el = activeScrollRef?.current;
    if (!el) {
      shouldStickToBottomRef.current = true;
      setShowJumpToLatest(false);
      return;
    }
    let lastScrollTop = el.scrollTop;
    // CUSTOM(G-AU): 滚动事件原先**每个事件**都跑一遍 refreshCurrentUserMessageIndex
    // （querySelectorAll 全部 user 节点 + 逐个 getBoundingClientRect + setState），
    // 没有任何节流 —— 在「往上翻历史」这条路径上就是连续的主线程阻塞。
    // 合并到一帧一次：滚动体感不变，但 DOM 查询从「每事件」降到「每帧」。
    let stickinessFrame: number | null = null;
    const updateStickiness = () => {
      if (stickinessFrame !== null) return;
      stickinessFrame = window.requestAnimationFrame(() => {
        stickinessFrame = null;
        const viewportGap = window.visualViewport
          ? window.innerHeight - window.visualViewport.height - window.visualViewport.offsetTop
          : 0;
        const rawDistanceFromBottom = el.scrollHeight - el.clientHeight - el.scrollTop;
        const distanceFromBottom = Math.max(0, rawDistanceFromBottom - viewportGap);
        const isNearBottom = distanceFromBottom < 40;
        const movedUp = el.scrollTop < lastScrollTop;
        const movedDown = el.scrollTop > lastScrollTop;
        if (isNearBottom) {
          shouldStickToBottomRef.current = true;
        } else if (movedUp) {
          shouldStickToBottomRef.current = false;
        } else if (movedDown && distanceFromBottom < 200) {
          shouldStickToBottomRef.current = true;
        }
        setShowJumpToLatest(!shouldStickToBottomRef.current);
        refreshCurrentUserMessageIndex();
        lastScrollTop = el.scrollTop;
      });
    };
    updateStickiness();
    el.addEventListener("scroll", updateStickiness, { passive: true });
    return () => {
      if (stickinessFrame !== null) {
        window.cancelAnimationFrame(stickinessFrame);
        stickinessFrame = null;
      }
      el.removeEventListener("scroll", updateStickiness);
    };
  }, [refreshCurrentUserMessageIndex, sessionKey]);

  useTargetSeqScroll({
    rootId, sessionKey, sessionNodeId, targetSeq, targetSeqRequestKey, timeline, windowMeta,
    activeScrollRef, targetSeqScrollKeyRef, shouldStickToBottomRef, targetSeqFrameRef, targetSeqTimerRefs,
    cancelTargetSeqScroll, setVisibleExchanges, setVisibleAux, setWindowMeta,
  });

  const {
    relatedFiles,
    relatedFileStatsByKey,
    gitStatsRefreshKey,
    scrollToRelatedFilesDivider,
    activeAskUserCallId,
    relatedFilesCollapsed,
    setRelatedFilesCollapsed,
    relatedFilesDividerRef,
    displayFileGroups,
  } = useRelatedFiles({
    session,
    timeline,
    isAwaiting,
    rootId,
    rootPath,
    gitFileStatsByPath,
    sessionNodeId,
    isMobile,
    t,
    interactionMode,
    activeScrollRef,
  });

  if (!session) {
    return (
      <div
        style={{
          padding: "40px",
          textAlign: "center",
          color: "var(--text-secondary)",
        }}
      >
        {t("session.empty")}
      </div>
    );
  }

  const displayName =
    session.name ||
    session.purpose ||
    session.key ||
    session.session_key ||
    "Session";
  const hasVisibleTimeline = timeline.length > 0;
  const themeColor = String(rootColor || "").trim() || "var(--accent-color)";
  // 四横主按钮：浅且暗的 muted 变体（同色相、混灰降明度）；徽标保持纯主题色
  const themeMuted = `color-mix(in srgb, ${themeColor} 56%, #94a3b8)`;
  const themeMutedBorder = `color-mix(in srgb, ${themeColor} 20%, transparent)`;
  const userMetaButtonStyle = buildUserMetaButtonStyle(themeColor);

  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        background: "transparent",
      }}
    >
      {interactionMode === "drawer" ? null : (
        <SessionViewerHeader interactionMode={interactionMode} rootId={rootId} rootColor={rootColor} rootDisplayName={rootDisplayName} displayName={displayName} onRootClick={onRootClick} />
      )}

      {/* 滚动容器 */}
      <div style={{ flex: 1, minHeight: 0, minWidth: 0, position: "relative" }}>
        <div ref={scrollRef} style={{ flex: 1, minHeight: 0, minWidth: 0, height: "100%", overflowY: "auto", overflowX: "hidden", position: "relative", WebkitOverflowScrolling: "touch" }}>
          <div style={{
            width: "100%",
            minWidth: 0,
            display: "block",
            padding: "24px 16px",
            boxSizing: "border-box",
            overflowX: "hidden",
          }} data-mindfs-session-content-width="1">
          <div style={{ width: "100%", minWidth: 0, margin: "0", display: "flex", flexDirection: "column" }}>
            {/* 方案 B：顶部哨兵，进入视口触发历史段加载（IntersectionObserver） */}
            <div ref={topSentinelRef} style={{ height: 1 }} aria-hidden="true" />
            <SessionViewerLoadingState loadingMore={loadingMore} loading={loading} hasVisibleTimeline={hasVisibleTimeline} />
            <SessionViewerTimeline timeline={timeline} timelineItemMeta={timelineItemMeta} locale={locale} isStreaming={isStreaming} rootId={rootId} rootPath={rootPath} sessionKey={sessionKey} sessionAgent={session?.agent} activeAskUserCallId={activeAskUserCallId} onAskUserAnswer={onAskUserAnswer} agents={agents} onFileClickRef={onFileClickRef} onEditUserMessage={onEditUserMessage} onForkAgentMessage={onForkAgentMessage} savedPromptKeys={savedPromptKeys} copiedMessageKeys={copiedMessageKeys} copyResetTimersRef={copyResetTimersRef} setSavedPromptKeys={setSavedPromptKeys} setCopiedMessageKeys={setCopiedMessageKeys} t={t} themeColor={themeColor} userMetaButtonStyle={userMetaButtonStyle} />
            {SessionViewerSlashCommandResult({
              slashCommandResult,
              hasVisibleTimeline,
              copiedMessageKeys,
              setCopiedMessageKeys,
              copyResetTimersRef,
              onFileClickRef,
              rootId,
              themeColor,
            })}
            <SessionViewerWaitingIndicator
              isAwaiting={isAwaiting} isStreaming={isStreaming}
              streamStatusText={streamStatusText} rootColor={rootColor} t={t}
            />

            <SessionViewerRelatedFiles
              relatedFiles={relatedFiles} relatedFileStatsByKey={relatedFileStatsByKey}
              gitFileStatsByPath={gitFileStatsByPath} relatedFilesCollapsed={relatedFilesCollapsed}
              setRelatedFilesCollapsed={setRelatedFilesCollapsed} relatedFilesDividerRef={relatedFilesDividerRef}
              displayFileGroups={displayFileGroups} rootPath={rootPath} onFileClickRef={onFileClickRef}
              onRemoveRelatedFile={onRemoveRelatedFile} interactionMode={interactionMode} t={t}
              relatedFileStatKey={relatedFileStatKey}
            />
            <div ref={scrollEndRef} style={{ height: "1px" }} />
          </div>
          </div>
        </div>
        {<SessionViewerFloatingControls
          userMessageSummaries={userMessageSummaries} showJumpToLatest={showJumpToLatest} sessionKey={sessionKey}
          composerOverlayInset={composerOverlayInset} cancelTargetSeqScroll={cancelTargetSeqScroll}
          targetSeq={targetSeq} targetSeqScrollKeyRef={targetSeqScrollKeyRef} targetSeqRequestKey={targetSeqRequestKey}
          shouldStickToBottomRef={shouldStickToBottomRef} setShowJumpToLatest={setShowJumpToLatest}
          stickSessionToBottom={stickSessionToBottom} t={t} themeColor={themeColor}
          userSummaryRootRef={userSummaryRootRef} refreshCurrentUserMessageIndex={refreshCurrentUserMessageIndex}
          setUserSummaryHoverOpen={setUserSummaryHoverOpen} userSummaryOpen={userSummaryOpen}
          windowMeta={windowMeta} visibleExchanges={visibleExchanges} userSummaryListRef={userSummaryListRef}
          currentUserMessageIndex={currentUserMessageIndex} scrollToUserMessageSummary={scrollToUserMessageSummary}
          setUserSummaryPinnedOpen={setUserSummaryPinnedOpen} themeMuted={themeMuted} themeMutedBorder={themeMutedBorder}
        />}
      </div>
      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.5; }
        }
        @keyframes sessionLoadingPulse {
          0% { background-position: 100% 0; opacity: 0.7; }
          50% { opacity: 1; }
          100% { background-position: -100% 0; opacity: 0.7; }
        }
        @keyframes spin {
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}

export const SessionViewer = memo(
  SessionViewerInner,
  (prev, next) =>
    prev.session === next.session &&
    prev.loading === next.loading &&
    prev.rootId === next.rootId &&
    prev.rootPath === next.rootPath &&
    prev.interactionMode === next.interactionMode &&
    prev.targetSeq === next.targetSeq &&
    prev.targetSeqRequestKey === next.targetSeqRequestKey &&
    prev.composerOverlayInset === next.composerOverlayInset &&
    prev.gitFileStatsByPath === next.gitFileStatsByPath &&
    prev.onRootClick === next.onRootClick,
);
