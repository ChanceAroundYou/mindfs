import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSessionStream, type TimelineItem } from "../../hooks/useSessionStream";
import type { TodoUpdate } from "../../services/session";
import { ViewHeader } from "../../layout/ViewHeader";
import { ThinkingBlock } from "../stream/ThinkingBlock";
import { ToolCallCard, renderToolIcon } from "../stream/ToolCallCard";
import { ToolCallGroupCard } from "../stream/ToolCallGroupCard";
import { AgentIcon } from "../agent/AgentIcon";
import { InlineTokenText } from "../common/InlineTokenText";
import { MarkdownViewer } from "../file/MarkdownViewer";
import { fetchProofProtectedBlob } from "../../services/file";
import { getRootNodeId } from "../../services/net/rootNode";
import { SESSION_WINDOW_SIZE, clearWindowedView, getSessionWindow, isPersistedSeq, mergeSessionExchanges, setWindowedView, type ExchangeAux, type RelatedFile, type SessionWindowMeta, type TokenUsage, type ToolCall } from "../../services/session";
import { savePrompt } from "../../services/prompts";
import { reportError } from "../../services/net/error";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { copyText } from "../../services/platform/clipboard";
import type { AgentStatus } from "../../services/agents";
import { useI18n, type Locale } from "../../i18n/index";
import { formatSessionDuration } from "../../services/sessionDuration";
import {
  computeTailOverlay,
  type OverlayExchange,
} from "../../shared/sessionOverlay";
import {
  relatedFileStatKey,
  useRelatedFileStats,
} from "../../hooks/useRelatedFileStats";

import { AttachmentImage } from "./SessionViewerAttachments";
import {
  ContextWindowBadge,
  TurnTokenUsage,
} from "./SessionViewerTokenViews";
import {
  CompactNoticeCard,
  PlanUpdateCard,
  TodoUpdateCard,
  UserMessageListIcon,
} from "./SessionViewerCards";
import { AskUserQuestionCard, ForkIcon, getAskUserQuestions } from "./AskUserQuestionCard";
import {
  extractUploadAttachments,
  formatAssistantExchangeMeta,
  formatTime,
  formatToolCallFallbackResult,
  normalizeUserMessageSummary,
  shouldDefaultCollapseRelatedFiles,
  stripImageAttachmentTokens,
  stripUploadAttachmentTokens,
  timelineItemSpacing,
} from "./sessionViewerFormat";
import type { ExchangeArray, SessionItem, SessionViewerProps } from "./sessionViewerTypes";
import { SessionViewerSlashCommandResult } from "./SessionViewerSlashCommand";
import { renderToolTimelineItem } from "./SessionViewerToolItem";
import { renderAuxiliaryTimelineItem } from "./SessionViewerAuxiliaryTimeline";
import { SessionViewerThoughtItem } from "./SessionViewerThoughtItem";
import { SessionViewerUserMessage } from "./SessionViewerUserMessage";
import { SessionViewerAssistantMessage } from "./SessionViewerAssistantMessage";
import { SessionViewerRelatedFiles } from "./SessionViewerRelatedFiles";
import { SessionViewerHeader } from "./SessionViewerHeader";
import { SessionViewerLoadingState } from "./SessionViewerLoadingState";
import { SessionViewerWaitingIndicator } from "./SessionViewerWaitingIndicator";
import { SessionViewerFloatingControls } from "./SessionViewerFloatingControls";
import { buildUserMetaButtonStyle } from "./sessionViewerStyles";
import { groupRelatedFiles } from "./sessionViewerGrouping";

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
  const [relatedFilesCollapsed, setRelatedFilesCollapsed] = useState(true);
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
  const relatedFilesDefaultStateRef = useRef<string>("");
  const userSummaryRootRef = useRef<HTMLDivElement | null>(null);
  const userSummaryListRef = useRef<HTMLDivElement | null>(null);
  const relatedFilesDividerRef = useRef<HTMLDivElement | null>(null);
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
  const [latestSeqState, setLatestSeqState] = useState<{ key: string; max: number }>(
    { key: "", max: 0 },
  );
  const noteLatestSeq = useCallback(
    (key: string | null, meta: SessionWindowMeta | null | undefined) => {
      const max = Number(meta?.maxSeq || 0);
      if (!key || max <= 0) return;
      setLatestSeqState((prev) =>
        prev.key === key ? (max > prev.max ? { key, max } : prev) : { key, max },
      );
    },
    [],
  );
  const latestSeq = latestSeqState.key === sessionKey ? latestSeqState.max : 0;
  // overlay 尾巴：**判定逻辑已搬到 `sessionOverlay.ts` 的纯函数**（本处只做接线）。
  // 搬出去的原因见那个文件的头部注释：它住在组件里时测不了，测试只能写同构副本，
  // 而副本挡住了冲突②（渲染空洞）—— `tests/session-window.test.mjs` 里那份副本可以随之删除。
  const tailOverlay = useMemo(
    () =>
      computeTailOverlay({
        exchanges: session?.exchanges as OverlayExchange[] | undefined,
        visibleExchanges: visibleExchanges as OverlayExchange[],
      }),
    [session?.exchanges, sessionKey, visibleExchanges],
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
  const [userSummaryHoverOpen, setUserSummaryHoverOpen] = useState(false);
  const [userSummaryPinnedOpen, setUserSummaryPinnedOpen] = useState(false);
  const [currentUserMessageIndex, setCurrentUserMessageIndex] = useState(0);
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
    setUserSummaryHoverOpen(false);
    setUserSummaryPinnedOpen(false);
    setCurrentUserMessageIndex(0);
    relatedFilesDefaultStateRef.current = "";
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
      // applyWindow 只服务「最新窗口」（init / anchor）——loadMore 与 targetSeq 取窗直接
      // setWindowMeta，不经过这里，因此 latestSeq 不会被旧/中段窗口的 maxSeq 污染。
      noteLatestSeq(sessionKey, res.meta);
      return true;
    },
    [noteLatestSeq, sessionKey],
  );

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

  const userMessageSummaries = useMemo(
    () =>
      timeline
        .filter((item): item is TimelineItem & { type: "user_text"; id: string; content: string } => item.type === "user_text")
        .map((item, index) => ({
          id: item.id || `user-${index}`,
          index: index + 1,
          summary: normalizeUserMessageSummary(item.content || "", t("session.emptyMessage")),
        })),
    [timeline, t],
  );
  const userSummaryOpen = userSummaryHoverOpen || userSummaryPinnedOpen;

  const readCurrentUserMessageIndex = useCallback(() => {
    const container = activeScrollRef?.current;
    if (!container) {
      return 0;
    }
    const nodes = Array.from(
      container.querySelectorAll<HTMLElement>("[data-user-message-index]"),
    );
    if (nodes.length === 0) {
      return 0;
    }
    const containerRect = container.getBoundingClientRect();
    const viewportTop = containerRect.top;
    const viewportBottom = containerRect.bottom;
    let firstVisible = 0;
    let firstFullyEntered = 0;
    let lastBeforeViewport = 0;
    for (const node of nodes) {
      const index = Number(node.dataset.userMessageIndex || 0);
      if (!index) {
        continue;
      }
      const rect = node.getBoundingClientRect();
      if (rect.bottom < viewportTop) {
        lastBeforeViewport = index;
        continue;
      }
      if (rect.top > viewportBottom) {
        break;
      }
      const isVisible = rect.bottom >= viewportTop && rect.top <= viewportBottom;
      if (isVisible && !firstVisible) {
        firstVisible = index;
      }
      if (rect.top >= viewportTop && !firstFullyEntered) {
        firstFullyEntered = index;
      }
    }
    return (
      firstFullyEntered ||
      firstVisible ||
      lastBeforeViewport ||
      Number(nodes[0]?.dataset.userMessageIndex || 0)
    );
  }, []);

  const refreshCurrentUserMessageIndex = useCallback(() => {
    setCurrentUserMessageIndex(readCurrentUserMessageIndex());
  }, [readCurrentUserMessageIndex]);

  const scrollToUserMessageSummary = (index: number) => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-user-message-index="${index}"]`,
    );
    if (!node) {
      return;
    }
    shouldStickToBottomRef.current = false;
    cancelTargetSeqScroll();
    const containerRect = container.getBoundingClientRect();
    const nodeRect = node.getBoundingClientRect();
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    const nextTop = Math.max(
      0,
      Math.min(
        maxTop,
        container.scrollTop +
          nodeRect.top -
          containerRect.top -
          container.clientHeight / 2 +
          nodeRect.height / 2,
      ),
    );
    container.scrollTop = nextTop;
    setShowJumpToLatest(nextTop < maxTop - 40);
    setUserSummaryPinnedOpen(false);
    setUserSummaryHoverOpen(false);
    setCurrentUserMessageIndex(index);
  };

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const nextIndex = readCurrentUserMessageIndex();
    setCurrentUserMessageIndex(nextIndex);
    if (!nextIndex) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const list = userSummaryListRef.current;
      const item = list?.querySelector<HTMLElement>(
        `[data-user-summary-index="${nextIndex}"]`,
      );
      item?.scrollIntoView({ block: "center" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [readCurrentUserMessageIndex, userSummaryOpen, userMessageSummaries.length]);

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const handlePointerDown = (event: PointerEvent) => {
      const root = userSummaryRootRef.current;
      if (!root || root.contains(event.target as Node)) {
        return;
      }
      setUserSummaryPinnedOpen(false);
      setUserSummaryHoverOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [userSummaryOpen]);

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

  useEffect(() => {
    if (!targetSeq) {
      targetSeqScrollKeyRef.current = "";
      return;
    }
    if (!sessionKey) {
      return;
    }
    const scrollKey = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
    if (targetSeqScrollKeyRef.current === scrollKey) {
      return;
    }
    const container = activeScrollRef?.current;
    if (!container || !timeline.length) {
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-session-seq="${targetSeq}"]`,
    );
    const inRange =
      windowMeta.minSeq > 0 &&
      targetSeq >= windowMeta.minSeq &&
      targetSeq <= windowMeta.maxSeq;
    if (node && inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      const scrollToNode = () => {
        const latestContainer = activeScrollRef?.current;
        const latestNode = latestContainer?.querySelector<HTMLElement>(
          `[data-session-seq="${targetSeq}"]`,
        );
        if (!latestContainer || !latestNode) {
          return;
        }
        shouldStickToBottomRef.current = false;
        const nextTop = Math.max(
          0,
          latestNode.offsetTop -
            latestContainer.clientHeight / 2 +
            latestNode.offsetHeight / 2,
        );
        latestContainer.scrollTo({ top: nextTop, behavior: "auto" });
      };
      targetSeqFrameRef.current = window.requestAnimationFrame(() => {
        targetSeqFrameRef.current = window.requestAnimationFrame(() => {
          targetSeqFrameRef.current = null;
          scrollToNode();
        });
      });
      [80, 220, 480].forEach((delay) => {
        const timer = window.setTimeout(() => {
          targetSeqTimerRefs.current = targetSeqTimerRefs.current.filter(
            (item) => item !== timer,
          );
          if (targetSeqScrollKeyRef.current !== scrollKey) {
            return;
          }
          scrollToNode();
        }, delay);
        targetSeqTimerRefs.current.push(timer);
      });
      return;
    }
    // 跨窗口：targetSeq 不在当前可见窗口内，拉取以 targetSeq 为中心的窗口再滚动。
    if (targetSeq > 0 && !inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      getSessionWindow(rootId || "", sessionKey, {
          beforeSeq: targetSeq + Math.floor(SESSION_WINDOW_SIZE / 2),
          limit: SESSION_WINDOW_SIZE,
          nodeId: sessionNodeId,
        })
        .then((res) => {
          if (!res) return;
          const winSession = res.session as any;
          const winExchanges = Array.isArray(winSession?.exchanges)
            ? (winSession.exchanges as ExchangeArray)
            : [];
          const winAux =
            (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
          setVisibleExchanges(winExchanges);
          setVisibleAux(winAux);
          setWindowMeta(res.meta);
          window.requestAnimationFrame(() => {
            const el = activeScrollRef?.current;
            const targetNode = el?.querySelector<HTMLElement>(
              `[data-session-seq="${targetSeq}"]`,
            );
            if (el && targetNode) {
              shouldStickToBottomRef.current = false;
              el.scrollTo({
                top: Math.max(
                  0,
                  targetNode.offsetTop -
                    el.clientHeight / 2 +
                    targetNode.offsetHeight / 2,
                ),
                behavior: "auto",
              });
            }
          });
        })
        .catch(() => {});
    }
  }, [sessionKey, targetSeq, targetSeqRequestKey, timeline, windowMeta]);

  const rawRelated = session?.related_files || (session as any)?.outputs || [];
  const relatedFiles = (Array.isArray(rawRelated) ? rawRelated : [])
    .map((f: any) => {
      const path =
        typeof f === "string" ? f : typeof f?.path === "string" ? f.path : "";
      const name =
        typeof f?.name === "string" ? f.name : path.split("/").pop() || path;
      const head =
        typeof f !== "string" && typeof f?.head === "string" ? f.head : "";
      const repoPath =
        typeof f !== "string" && typeof f?.repo_path === "string"
          ? f.repo_path
          : "";
      const repoName =
        typeof f !== "string" && typeof f?.repo_name === "string"
          ? f.repo_name
          : repoPath.split(/[\\/]/).filter(Boolean).pop() || "";
      const repoKind =
        typeof f !== "string" && typeof f?.repo_kind === "string"
          ? f.repo_kind
          : "";
      const rootID =
        typeof f !== "string" && typeof f?.root_id === "string"
          ? f.root_id
          : "";
      return { path, name, head, repo_path: repoPath, repo_name: repoName, repo_kind: repoKind, root_id: rootID };
    })
    .filter((f) => f.path);
  const gitStatsRefreshKey = Object.entries(gitFileStatsByPath)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([path, stats]) => `${path}:${stats.status}:${stats.additions}:${stats.deletions}`)
    .join("|");
  const relatedFileStatsByKey = useRelatedFileStats(
    rootId,
    relatedFiles,
    gitStatsRefreshKey,
    sessionNodeId,
  );
  const scrollToRelatedFilesDivider = (behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    let top = maxTop;
    const divider =
      interactionMode === "drawer" ? relatedFilesDividerRef.current : null;
    if (divider && relatedFiles.length > 0 && !relatedFilesCollapsed) {
      const dividerTop =
        divider.getBoundingClientRect().top -
        container.getBoundingClientRect().top +
        container.scrollTop;
      const target = dividerTop - container.clientHeight * 0.08;
      top = Math.max(0, Math.min(maxTop, target));
    }
    container.scrollTo({ top, behavior });
  };
  const activeAskUserCallId = (() => {
    if (!isAwaiting) {
      return "";
    }
    for (let i = timeline.length - 1; i >= 0; i -= 1) {
      const item = timeline[i];
      if (item.type === "user_text" || item.type === "assistant_text") {
        return "";
      }
      if (item.type !== "tool") continue;
      const toolCall = item.toolCall || {};
      const kind = `${toolCall.kind || ""}`.toLowerCase();
      const status = `${toolCall.status || ""}`.toLowerCase();
      if (kind !== "ask_user") continue;
      if (
        status !== "running" &&
        status !== "pending" &&
        status !== "in_progress"
      ) {
        continue;
      }
      if (getAskUserQuestions(toolCall).length === 0) continue;
      return (
        toolCall.callId ||
        (typeof toolCall.meta?.toolUseId === "string"
          ? toolCall.meta.toolUseId
          : "")
      );
    }
    return "";
  })();
  const defaultRelatedFilesCollapsed = shouldDefaultCollapseRelatedFiles(
    isMobile,
    relatedFiles.length,
  );
  const relatedFilesDefaultStateKey = `${sessionKey || ""}:${isMobile ? "mobile" : "desktop"}:${defaultRelatedFilesCollapsed ? "collapsed" : "expanded"}`;

  useEffect(() => {
    if (relatedFilesDefaultStateRef.current === relatedFilesDefaultStateKey) {
      return;
    }
    relatedFilesDefaultStateRef.current = relatedFilesDefaultStateKey;
    setRelatedFilesCollapsed(defaultRelatedFilesCollapsed);
  }, [defaultRelatedFilesCollapsed, relatedFilesDefaultStateKey]);

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

  const displayFiles = relatedFiles;
  const displayFileGroups = groupRelatedFiles(displayFiles, rootPath, t);
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

  const makePromptKey = (
    item: Extract<TimelineItem, { type: "user_text" | "assistant_text" }>,
  ): string =>
    `${item.id}\n${item.timestamp || ""}\n${item.content || ""}`;

  const renderTimelineItem = (
    item: TimelineItem,
    idx: number,
    spacing: string = "0",
  ) => {
    const timelineItemKey = item.id || `${item.type}-${idx}`;
    if (item.type === "thought") {
      return <SessionViewerThoughtItem item={item} timelineItemKey={timelineItemKey} spacing={spacing} />;
    }
    if (item.type === "tool") {
      return renderToolTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey, sessionAgent: session?.agent, activeAskUserCallId, onAskUserAnswer });
    }
    // CUSTOM(G-AT): tool_group 仍由本 fork 的辅助时间线渲染器统一处理。
    if (item.type === "tool_group" || item.type === "todo" || item.type === "plan" || item.type === "compact") {
      return renderAuxiliaryTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey });
    }
    const isUser = item.type === "user_text";
    // CUSTOM(G-AU): 查表，不再 slice+filter / 反向扫描（见 timelineItemMeta）。
    const itemMeta = timelineItemMeta[idx];
    const userMessageIndex = isUser ? itemMeta.userIndex : undefined;
    const next = idx + 1 < timeline.length ? timeline[idx + 1] : null;
    const hasFollowingAssistantFlow =
      !isUser && !!next && next.type !== "user_text";
    const hideAssistantMeta =
      !isUser &&
      (hasFollowingAssistantFlow ||
        (isStreaming && idx === timeline.length - 1));
    const time = formatTime(item.timestamp, locale);
    const uploadAttachments = isUser
      ? extractUploadAttachments(item.content || "")
      : [];
    const imageAttachments = uploadAttachments.filter(
      (attachment) => attachment.isImage,
    );
    const displayContent = isUser
      ? stripImageAttachmentTokens(item.content || "")
      : item.content || "";
    const promptSaveContent = isUser
      ? stripUploadAttachmentTokens(item.content || "")
      : "";
    const promptKey = makePromptKey(item);
    const promptSaved = !!savedPromptKeys[promptKey];
    const copySucceeded = !!copiedMessageKeys[promptKey];
    const userMessageWidth =
      imageAttachments.length > 0 ? "min(320px, 100%)" : "auto";
    const hasRichUserAttachments = imageAttachments.length > 0;
    const assistantMarkdownContent = !isUser ? (item.content || "").trim() : "";
    const assistantExchangeMeta = !isUser
      ? formatAssistantExchangeMeta(item, agents)
      : "";
    const assistantDurationLabel = !isUser
      ? formatSessionDuration(itemMeta.prevUserTs, item.timestamp)
      : "";
    const canForkAgentMessage = !isUser && isPersistedSeq(item.seq) && !!onForkAgentMessage;
    return (
      <div
        key={timelineItemKey}
        data-session-seq={item.seq || undefined}
        data-user-message-index={userMessageIndex}
        style={{
          marginTop: spacing,
          alignSelf: isUser ? "flex-end" : "flex-start",
          width: isUser ? userMessageWidth : "100%",
          maxWidth: isUser ? "80%" : "100%",
          minWidth: 0,
          position: "relative",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {isUser ? (
          <SessionViewerUserMessage
            {...{ item, time, spacing, timelineItemKey, rootId, rootPath, displayContent, promptSaveContent,
              promptKey, promptSaved, copySucceeded, imageAttachments, userMessageWidth, hasRichUserAttachments,
              onFileClickRef, onEditUserMessage, savePrompt, copyText, reportError, setSavedPromptKeys,
              setCopiedMessageKeys, copyResetTimersRef, pendingAck: item.pendingAck, t, themeColor, userMetaButtonStyle }}
          />
        ) : (
          <SessionViewerAssistantMessage
            {...{ item, time, spacing, timelineItemKey, rootId, rootPath, onFileClickRef, t, themeColor,
              userMetaButtonStyle, hideAssistantMeta, assistantMarkdownContent, canForkAgentMessage,
              onForkAgentMessage, assistantExchangeMeta, assistantDurationLabel, tokenUsage: item.tokenUsage,
              contextWindow: item.contextWindow }}
          />
        )}
      </div>
    );
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
            {timeline.map((item, idx) =>
              renderTimelineItem(
                item,
                idx,
                timelineItemSpacing(idx > 0 ? timeline[idx - 1] : null, item),
              ),
            )}
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
