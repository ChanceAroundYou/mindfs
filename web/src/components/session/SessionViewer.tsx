import React, { memo, useEffect, useMemo, useRef, useState } from "react";
import { useSessionStream, type TimelineItem } from "../../hooks/useSessionStream";
import { useTargetSeqScroll } from "./useTargetSeqScroll";
import { buildTimelineItemMeta } from "./buildTimelineItemMeta";
import { useSessionTimelineVirtualizer } from "./useSessionTimelineVirtualizer";
import { useSessionWindowApply } from "./useSessionWindowApply";
import { useSessionViewerScrollPositionEffects, useSessionViewerScrollState, useSessionViewerStickinessEffect } from "./useSessionViewerScroll";
import { useSelectionContainment } from "../../hooks/useSelectionContainment";
import type { TodoUpdate } from "../../shared/sessionTypes";
import { ViewHeader } from "../../layout/ViewHeader";
// 窗口取数（SESSION_WINDOW_SIZE / getSessionWindow / setWindowedView /
// clearWindowedView / mergeSessionExchanges 的另一半）已随 2026-10-08 的拆分
// 搬进 ./useSessionWindowing，这里只剩仍被重锚定与时间线用到的这几个。
import type { ExchangeAux, SessionBlock, SessionWindowMeta } from "../../shared/sessionTypes";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { copyText } from "../../platform/clipboard";
import type { AgentStatus } from "../../shared/agentTypes";
import { useI18n } from "../../i18n/index";
import { formatSessionDuration } from "../../shared/sessionDuration";
import {
  composeWindowAndOverlay,
  computeTailOverlay,
  supplementOverlayMap,
  type OverlayExchange,
} from "../../shared/sessionOverlay";
import {
  relatedFileStatKey,
  useRelatedFileStats,
} from "../../hooks/useRelatedFileStats";

import {
  extractUploadAttachments,
  formatTime,
  formatToolCallFallbackResult,
  normalizeUserMessageSummary,
  shouldDefaultCollapseRelatedFiles,
  stripImageAttachmentTokens,
  stripUploadAttachmentTokens,
} from "./sessionViewerFormat";
import type { ExchangeArray, SessionItem, SessionViewerProps } from "./sessionViewerTypes";
import { useSessionWindowing } from "./useSessionWindowing";
import { SessionViewerSlashCommandResult } from "./SessionViewerSlashCommandResult";
import { SessionViewerRelatedFiles } from "./SessionViewerRelatedFiles";
import { SessionViewerHeader } from "./SessionViewerHeader";
import { SessionViewerLoadingState } from "./SessionViewerLoadingState";
import { SessionViewerWaitingIndicator } from "./SessionViewerWaitingIndicator";
import { SessionViewerFloatingControls } from "./SessionViewerFloatingControls";
import { buildUserMetaButtonStyle, sessionViewerAnimationStyles } from "./sessionViewerStyles";
import { SessionViewerTimeline } from "./SessionViewerTimeline";
import { useUserMessageSummaries } from "./useUserMessageSummaries";
import { useRelatedFiles } from "./useRelatedFiles";
import { useSessionViewerExchangeProjection } from "./useSessionViewerExchangeProjection";
import type { ToolCallViewCache } from "../stream/useToolCallCard";

// CUSTOM(G-AT): tool_group 由时间线渲染器统一处理，保持 fork 的分组显示语义。

function SessionViewerInner({
  session,
  presence = "idle",
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
  scrollContainerRef, headerMenu = null,
}: SessionViewerProps) {
  const { locale, t } = useI18n();
  const { mergeVisibleExchanges, keepPersistedExchanges } = useSessionViewerExchangeProjection();
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
  // CUSTOM(G-MA)：拖选复制只认正文。向上拖出滚动容器时浏览器会把选区扩到会话
  // 标题栏和上方全部正文，这里把越界的那一端拉回本容器边界（见 shared/selectionClamp）。
  useSelectionContainment(scrollRef);
  const onFileClickRef = useRef(onFileClick);
  const copyResetTimersRef = useRef<Record<string, number>>({});
  const sessionKey = session?.key || session?.session_key || null;
  // CUSTOM(G-AT)：工具卡的展开态与已拉到的详情按会话存活，虚拟化卸载/重挂时才能恢复
  // （否则滚出视口再滚回来，展开的卡片合上、详情重新请求）。`976a5d0f` 合上游 v0.5.5 时
  // 这段随 `SessionViewer.tsx` 整体替换丢失，`useToolCallCard` 的 viewCache 参数成了死代码。
  const toolViewCache = useMemo<ToolCallViewCache>(
    () => ({ expanded: new Map(), details: new Map() }),
    [rootId, sessionKey],
  );
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
  // 持久行只认「比窗口最新一条更新」的：下界由 computeTailOverlay 从 visibleExchanges 现算，
  // 不持有水位状态。手点「同步」时回包是 afterSeq 之后的整段历史（缓存缺失时就是整个会话），
  // App 缓存会因此装着窗口以下的几百条行；若把「不在窗口里」当成「新尾巴」，它们会被全部
  // 追加到窗口之后，又因为不在 visibleBlocks 里、`content` 恒空而各自退化成 `turn_meta`
  // 纯页脚（复制 / 模型 / 用量 / 时间 / 上下文胶囊）—— 就是「同步后底部一大堆控件」。
  // 窗口以下的持久行一律交给 applyWindow（上翻分页）。
  //
  // 为什么不用 windowMeta 当这个下界：loadMore 与 targetSeq 取窗都会用旧/中段窗口的 meta
  // 覆盖它，拿它当「会话最新 seq」会塌回旧值（曾导致按位次裁剪错算并把实时流式内容一起
  // slice 掉）。瞬时行（seq==0）按 mindfs_id + 角色类别与窗口对账；文本相似不能证明相同。
  // 只读派生不写回缓存，避免覆盖其它标签页可能正在流式写入的持久化缓存。
  // 持久行按 seq；无身份的瞬时行继续保留，确保实时正文与思考不会被丢。
  const tailOverlay = useMemo(
    () =>
      computeTailOverlay({
        exchanges: session?.exchanges as OverlayExchange[] | undefined,
        visibleExchanges: visibleExchanges as OverlayExchange[],
      }),
    [session?.exchanges, visibleExchanges],
  );
  // CUSTOM(G-SJ)：合并规则（持久行按 seq 去重、瞬时行按对象同一性去重）收在
  // composeWindowAndOverlay 里；窗口态与 overlay 可能持有同一 seq 的不同对象。
  const composedExchanges = useMemo(
    () => composeWindowAndOverlay(visibleExchanges as OverlayExchange[], tailOverlay) as unknown as ExchangeArray,
    [visibleExchanges, tailOverlay],
  );
  // overlay 行的正文块 / aux 住在会话级映射里（窗口没拉过它们）。只补窗口缺的键 ——
  // 同一 seq 两个 revision 并存会让正文与工具卡重复。
  const overlaySeqs = useMemo(
    () => tailOverlay.map((ex) => Number(ex?.seq || 0)).filter((seq) => seq > 0),
    [tailOverlay],
  );
  const effectiveAux = useMemo(
    () => supplementOverlayMap(visibleAux, session?.exchange_aux, overlaySeqs) || visibleAux,
    [visibleAux, session?.exchange_aux, overlaySeqs],
  );
  const effectiveBlocks = useMemo(
    () =>
      supplementOverlayMap(
        visibleBlocks,
        session?.blocks as Record<string, SessionBlock[]> | undefined,
        overlaySeqs,
      ) || visibleBlocks,
    [visibleBlocks, session?.blocks, overlaySeqs],
  );
  const { timeline, isStreaming, streamVersion, streamStatusText } = useSessionStream(
    sessionKey,
    composedExchanges,
    effectiveAux,
    session?.context_window,
    isAwaiting,
    effectiveBlocks,
  );
  // CUSTOM(G-AU): 原先在 timeline.map 里对每个 user 项做「切前缀再 filter 数 user」、
  // 对每个 assistant 项做 previousUserTimestamp 反向扫描 —— 两处都是 O(n²)，
  // 且每轮渲染都重算（流式输出 33 次/秒）。改成一次 O(n) 前向扫描，渲染里只查表。
  const timelineItemMeta = useMemo(() => buildTimelineItemMeta(timeline as any), [timeline]);
  const scrollToRelatedFilesDividerRef = useRef<(behavior?: ScrollBehavior) => void>(() => {});
  // 时间线虚拟化（CUSTOM(G-JT)）：屏外条目不挂载；用户消息导航与 targetSeq 跳转
  // 都改走同一份索引，避免「画出来的」与「跳过去的」两套口径。
  const timelineNav = useSessionTimelineVirtualizer({
    timeline,
    timelineItemMeta,
    scrollRef,
    sessionKey,
  });
  const {
    shouldStickToBottomRef, lastSessionKeyRef, targetSeqScrollKeyRef,
    targetSeqFrameRef, targetSeqTimerRefs, showJumpToLatest,
    setShowJumpToLatest, viewportStickFrameRef, cancelTargetSeqScroll,
    stickSessionToBottom,
  } = useSessionViewerScrollState(activeScrollRef);

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

  const applyWindow = useSessionWindowApply({
    mergeVisibleExchanges,
    setVisibleExchanges,
    setVisibleAux,
    setVisibleBlocks,
    setWindowMeta,
  });

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
    // 锚点载荷是 App 的整份缓存：持久化行 + 尚未落盘的瞬时行（无正 seq，见 App.loadSession 的
    // [...incoming.filter(seq>0), ...localTransient]）。窗口态只能装持久化行 ——
    // 瞬时行归 overlay 管，混进窗口后 overlay 会再输出一遍，同一段正文渲染两次
    // （实测 2026-09-17：ask 触发后 ask 上下各一块同样的分析文本，重开会话必现）。
    const anchorExchanges = keepPersistedExchanges(Array.isArray((session as any)?.exchanges)
      ? ((session as any).exchanges as ExchangeArray)
      : ([] as ExchangeArray));
    applyWindow(
      {
        session: { ...(session as any), exchanges: anchorExchanges },
        meta: anchorMeta,
      },
      { keepOlder: true },
    );
  }, [session, sessionKey, applyWindow, keepPersistedExchanges]);

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

  useSessionViewerScrollPositionEffects({
    activeScrollRef, interactionMode, isPrependingRef, lastSessionKeyRef,
    scrollEndRef, scrollToRelatedFilesDividerRef, sessionKey,
    timelineLength: timeline.length,
    timelineTailId: (timeline as any[])[(timeline as any[]).length - 1]?.id || "",
    isStreaming, streamVersion, slashCommandResult, shouldStickToBottomRef,
    viewportStickFrameRef, stickSessionToBottom,
  });

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
    navigation: timelineNav,
  });

  useSessionViewerStickinessEffect({
    activeScrollRef, refreshCurrentUserMessageIndex, sessionKey,
    shouldStickToBottomRef, setShowJumpToLatest,
  });


  useTargetSeqScroll({
    rootId, sessionKey, sessionNodeId, targetSeq, targetSeqRequestKey, timeline, windowMeta,
    activeScrollRef, targetSeqScrollKeyRef, shouldStickToBottomRef, targetSeqFrameRef, targetSeqTimerRefs,
    cancelTargetSeqScroll, setVisibleExchanges, setVisibleAux, setWindowMeta,
    navigation: timelineNav,
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
  scrollToRelatedFilesDividerRef.current = scrollToRelatedFilesDivider;

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
  const userMetaButtonStyle = buildUserMetaButtonStyle(themeColor);
  const headerHistory = {
    userMessageSummaries, userSummaryOpen, userSummaryRootRef, userSummaryListRef,
    currentUserMessageIndex, refreshCurrentUserMessageIndex, scrollToUserMessageSummary,
    setUserSummaryHoverOpen, setUserSummaryPinnedOpen, windowMeta, visibleExchanges, themeColor,
  };

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
        <SessionViewerHeader interactionMode={interactionMode} rootId={rootId} rootColor={rootColor} rootDisplayName={rootDisplayName} displayName={displayName} isDelegating={!isAwaiting && presence === "delegating"} isAwaitingAnswer={!isAwaiting && presence === "awaiting"} onRootClick={onRootClick} menu={headerMenu} history={headerHistory} />
      )}

      {/* 滚动容器 */}
      <div style={{ flex: 1, minHeight: 0, minWidth: 0, position: "relative" }}>
        <div ref={scrollRef} style={{ flex: 1, minHeight: 0, minWidth: 0, height: "100%", overflowY: "auto", overflowX: "hidden", position: "relative", WebkitOverflowScrolling: "touch", overscrollBehaviorY: "contain", scrollPaddingBottom: `${composerOverlayInset + 20}px` }}>
          <div style={{
            width: "100%",
            minWidth: 0,
            display: "block",
            padding: "24px 16px",
            boxSizing: "border-box",
            overflowX: "hidden",
            // CUSTOM(G-MA)：正文始终可选中——即使外层（header / 侧栏）配了
            // user-select: none，也不会顺着继承把正文一起禁掉。
            userSelect: "text",
          }} data-mindfs-session-content-width="1">
          <div style={{ width: "100%", minWidth: 0, margin: "0", display: "flex", flexDirection: "column" }}>
            {/* 方案 B：顶部哨兵，进入视口触发历史段加载（IntersectionObserver） */}
            <div ref={topSentinelRef} style={{ height: 1 }} aria-hidden="true" />
            <SessionViewerLoadingState loadingMore={loadingMore} loading={loading} hasVisibleTimeline={hasVisibleTimeline} />
            <SessionViewerTimeline timeline={timeline} timelineItemMeta={timelineItemMeta} locale={locale} isStreaming={isStreaming} rootId={rootId} rootPath={rootPath} sessionKey={sessionKey} sessionAgent={session?.agent} activeAskUserCallId={activeAskUserCallId} onAskUserAnswer={onAskUserAnswer} agents={agents} onFileClickRef={onFileClickRef} onEditUserMessage={onEditUserMessage} onForkAgentMessage={onForkAgentMessage} savedPromptKeys={savedPromptKeys} copiedMessageKeys={copiedMessageKeys} copyResetTimersRef={copyResetTimersRef} setSavedPromptKeys={setSavedPromptKeys} setCopiedMessageKeys={setCopiedMessageKeys} t={t} themeColor={themeColor} userMetaButtonStyle={userMetaButtonStyle} virtual={timelineNav.virtual} toolViewCache={toolViewCache} />
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
              isAwaiting={isAwaiting} isStreaming={isStreaming} isDelegating={!isAwaiting && presence === "delegating"} isAwaitingAnswer={!isAwaiting && presence === "awaiting"}
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
          showJumpToLatest={showJumpToLatest} sessionKey={sessionKey}
          composerOverlayInset={composerOverlayInset} cancelTargetSeqScroll={cancelTargetSeqScroll}
          targetSeq={targetSeq} targetSeqScrollKeyRef={targetSeqScrollKeyRef} targetSeqRequestKey={targetSeqRequestKey}
          shouldStickToBottomRef={shouldStickToBottomRef} setShowJumpToLatest={setShowJumpToLatest}
          stickSessionToBottom={stickSessionToBottom} t={t} themeColor={themeColor}
        />}
      </div>
      <style>{sessionViewerAnimationStyles}</style>
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
