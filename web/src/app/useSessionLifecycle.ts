/**
 * 会话生命周期与选择 —— A1a 簇（8 个单元，~900 行）。
 *
 * 覆盖：恢复活动会话、选中会话、同步/重命名会话、按根加载会话列表、
 * 绑定会话展示、子会话加载、列表项头同步。
 *
 * 同簇另有 3 个单元（handleDeleteSession / handleArchiveSession /
 * showBoundSessionOrRootDir）依赖 L3073 之后才声明的闭包，会破坏 TDZ 窗口，
 * 留待后续单独抽取。
 *
 * 做法：hook，参数面 UseSessionLifecycleParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { CHILD_SESSION_PAGE_SIZE, useResponsive, waitForNextPaint } from "./appMisc";
import { MainViewMode, URLState, buildDirectorySelectionKey } from "./appPath";
import { Exchange, MultiProjectSessionGroup, SessionItem, WSStatus, toSessionItem } from "./appSession";
import { useActionHandlers } from "./useActionHandlers";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionCache } from "./useSessionCache";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { SessionViewer } from "../components/session/SessionViewer";
import { useI18n } from "../i18n";
import { type FilePayload } from "../services/file";
import type { UseSessionLifecycleParams } from "./useSessionLifecycleTypes";
import { type GitDiffPayload } from "../services/git";
import { reportError } from "../services/net/error";
import { type FileEntry } from "../services/prefs/directorySort";
import { type Session, SESSION_WINDOW_SIZE, clearWindowedView, composeLoadedExchanges, getCachedSession, getCachedSessionList, getSessionWindow, isTransientExchange, saveCachedSessionList, sessionService, setWindowedView, syncSession } from "../services/session";
import { mergeSessionItems } from "../services/sessionListMerge";

export function useSessionLifecycle(params: UseSessionLifecycleParams) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const {
    anchorSeqRef,
    applyPendingToMultiProjectGroups,
    applyPinSnapshot,
    boundSessionByRootRef,
    bumpCacheVersion,
    clearLocalPendingForSession,
    clearSessionStale,
    currentRootId,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerSessionByRootRef,
    enforceSessionCacheBound,
    file,
    fileCursorRef,
    getNodeIdForRoot,
    isSessionStale,
    listLoadErrorAtRef,
    loadedSessionRef,
    loadingSessionRef,
    mainViewRef,
    multiProjectPendingRef,
    pluginQuery,
    replaceURLState,
    resolvePendingForSession,
    rootSessionKey,
    scopedRootKey,
    selectRootNode,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheAccessRef,
    sessionCacheRef,
    sessionListAbortRef,
    sessionListLoadSeqRef,
    sessionLoadFailureRef,
    sessionSearchTargetCounterRef,
    sessions,
    setBoundSessionForRoot,
    setCurrentRootId,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setFile,
    setGitDiff,
    setHasMoreSessions,
    setInteractionMode,
    setIsLeftOpen,
    setIsRightOpen,
    setMainEntries,
    setMultiProjectSessionGroups,
    setPluginQuery,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedKanbanTaskId,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessions,
    setSyncingSessionKeys,
    status,
    switchMainView,
  } = params;

  const restoreActiveSession = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
    ): Promise<Session | null> => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey || resolvedKey.startsWith("pending-")) {
        return null;
      }
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      const cachedBeforeSync = sessionCacheRef.current[cacheKey];
      // 加载前内存缓存里的行 —— 「在途内容（seq=0）」的唯一来源。
      // 服务端不下发它们（窗口回包只有已落盘行），所以每次加载都必须从这份快照接回，
      // 见 session.ts 的 composeLoadedExchanges。
      const cachedExchangesBefore: Exchange[] = Array.isArray(
        (cachedBeforeSync as any)?.exchanges,
      )
        ? ((cachedBeforeSync as any).exchanges as Exchange[])
        : [];
      // 这里**不需要**任何「事件进度」的簿记了（2026-10-06）。
      //
      // 曾经此处有一段 `clearEventCursor`，以及一段论证它「不能清」的注释 —— 那都是
      // 「客户端自报进度、服务端只补发缺的那几条」那套续传协议的残余。协议本身已删：
      // 挂载会话一律是**快照重建**（服务端回一整批带 `reset:true` 的事件，客户端先清
      // 瞬时尾巴再照单应用），所以「我收到哪儿了」不再是任何一方的输入。
      //
      // 顺序上这里也没问题：本函数在窗口/sync 结果**并入缓存之后**才调 markSessionReady
      // （下面 :2337 / :2396），所以重置帧总是落在组装好的缓存之上，而不是被它覆盖。
      const inflight = loadingSessionRef.current[cacheKey] as unknown as Promise<Session | null> | undefined;
      if (inflight) {
        const hit = await inflight;
        return hit;
      }
      const promise = (async (): Promise<Session | null> => {
        // 方案 B 首屏按需：先尝试窗口化拉取最新 50 条，失败回退全量 syncSession
        try {
          setWindowedView(resolvedKey, true);
          const win = await getSessionWindow(resolvedRoot, resolvedKey, {
            latest: SESSION_WINDOW_SIZE,
            nodeId: getNodeIdForRoot(resolvedRoot),
          });
          if (win && (win as any).session) {
            const sess: any = {
              ...(win as any).session,
              key: resolvedKey,
              session_key: resolvedKey,
            };
            const serverPending =
              typeof sess?.pending === "boolean" ? !!sess.pending : undefined;
            if (serverPending === false) {
              clearLocalPendingForSession(resolvedRoot, resolvedKey);
            }
            const pending =
              serverPending === false
                ? false
                : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
            const anchorAt = ++anchorSeqRef.current;
            // 窗口**并入**缓存，不是**替换**缓存。替换就是「切会话闪一下」的
            // 第二来源（2026-10-05）：窗口只有尾部 SESSION_WINDOW_SIZE 条，
            // 而缓存里可能有几百条（用户翻上去了、或 live 路径落库过）。
            // 整体覆盖会把头部历史当场扔掉，切回来时首帧种子只剩 20 条 ——
            // 于是「完整对话 → 塌成最后一条用户消息 → 慢慢补回来」。
            // 淘汰只在**写入**时发生、且只从头部淘汰（见 toPersistentSession）；
            // 加载过程中一律只增不减。
            const winExs = Array.isArray(sess.exchanges)
              ? (sess.exchanges as Exchange[])
              : [];
            const toCache = {
              ...sess,
              key: resolvedKey,
              // CUSTOM(G-Z): 窗口/列表回包不带 root_id（root 只是请求参数），
              // 这里不补，缓存里就是「无主」会话 —— 二次跳转时归属会退化成当前根。
              root_id: resolvedRoot,
              pending,
              // 加载时的唯一组装规则：服务端行并入缓存，在途时再把 seq=0 接回。
              // 判据是 `pending` —— 不在跑就没有在途内容，缓存里的 seq=0 全是残留
              // （跨多次 compact 堆起来的那种，渲染出来就是「同一段正文两遍」）。
              exchanges: composeLoadedExchanges(winExs, cachedExchangesBefore, pending),
              // aux 同理并入：exchanges 保住历史而 exchange_aux 只剩窗口那份，
              // 那些行上的工具卡会凭空消失（历史里的 read/edit 变纯文本）。
              // 按 seq 覆盖而非拼接 —— 同一个 seq 被窗口与缓存各带一份时，
              // 拼接会把同一张卡渲染两遍（appendExchangeAuxDelta 是拼接语义，不能用）。
              exchange_aux: {
                ...((cachedBeforeSync as any)?.exchange_aux || {}),
                ...(sess.exchange_aux || {}),
              },
              _windowMeta: win.meta,
              _anchoredAt: anchorAt,
            } as Session;
            sessionCacheRef.current[cacheKey] = toCache;
            // CUSTOM(G-AU): 新键入缓存时顺手封顶（见 enforceSessionCacheBound）。
            sessionCacheAccessRef.current[cacheKey] = Date.now();
            enforceSessionCacheBound();
            bumpCacheVersion();
            await sessionService.markSessionReady(resolvedRoot, resolvedKey);
            return toCache;
          }
          // 窗口未命中则清标记，走全量回退
          clearWindowedView(resolvedKey);
        } catch {
          clearWindowedView(resolvedKey);
        }
        // 回退：全量 syncSession（保持原有 pending/转瞬态逻辑）
        const syncResult = await syncSession(resolvedRoot, resolvedKey, {
          nodeId: getNodeIdForRoot(resolvedRoot),
        });
        let fullSession = syncResult?.session as any;
        if (!fullSession) {
          return null;
        }
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        if (serverPending === false) {
          clearLocalPendingForSession(resolvedRoot, resolvedKey);
        }
        const pending =
          serverPending === false
            ? false
            : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
        // 与窗口分支同一条组装规则。syncSession 的结果来自 IDB 缓存 + 服务端增量，
        // 它不知道内存里正在流式输出的那部分 —— 必须由这里接回（实测 2026-10-06：
        // 服务端只回 15 条已落盘行，内存缓存有 376 条，其中 361 条是在途内容）。
        // 判据是 `pending`：不在跑就没有在途内容，缓存里的 seq=0 全是残留。
        fullSession = {
          ...fullSession,
          exchanges: composeLoadedExchanges(
            (fullSession as any).exchanges,
            cachedExchangesBefore,
            pending,
          ),
        } as Session;
        const anchorAt = ++anchorSeqRef.current;
        const anchoredExs = Array.isArray((fullSession as any)?.exchanges)
          ? ((fullSession as any).exchanges as any[])
          : [];
        const anchoredMeta = {
          total: anchoredExs.length,
          hasMore: false,
          minSeq: anchoredExs.length ? Number(anchoredExs[0]?.seq || 0) : 0,
          maxSeq: anchoredExs.length
            ? Number(anchoredExs[anchoredExs.length - 1]?.seq || 0)
            : 0,
        };
        sessionCacheRef.current[cacheKey] = {
          ...(fullSession as any),
          key: resolvedKey,
          // CUSTOM(G-Z): 同窗口分支 —— 全量 sync 回包同样不带 root_id。
          root_id: resolvedRoot,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
        // CUSTOM(G-AU): 新键入缓存时顺手封顶（见 enforceSessionCacheBound）。
        sessionCacheAccessRef.current[cacheKey] = Date.now();
        enforceSessionCacheBound();
        bumpCacheVersion();
        await sessionService.markSessionReady(resolvedRoot, resolvedKey);
        return {
          ...(fullSession as any),
          key: resolvedKey,
          root_id: resolvedRoot,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
      })();
      loadingSessionRef.current[cacheKey] = promise as any;
      try {
        return await promise;
      } catch (err) {
        // CUSTOM(G-AU): 失败必须留下痕迹。原先异常原样抛出，而调用点只写 .then 不写
        // .catch —— 失败既不改状态也不留记录，和「还没加载」完全等价，下一轮依赖一变
        // 就再拉一次。404 记成永久（会话确实不存在，重试无意义），其余错误退避 5s。
        const status = (err as { status?: number } | null)?.status || 0;
        sessionLoadFailureRef.current[cacheKey] = { at: Date.now(), status };
        return null;
      } finally {
        delete loadingSessionRef.current[cacheKey];
      }
    },
    [
      bumpCacheVersion,
      clearLocalPendingForSession,
      enforceSessionCacheBound,
      resolvePendingForSession,
      rootSessionKey,
    ],
  );
  const syncSessionHeaderFromListItem = useCallback(
    (rootID: string, item: SessionItem | null | undefined) => {
      const sessionKey = item?.key || item?.session_key || "";
      const sessionName = typeof item?.name === "string" ? item.name : "";
      if (!rootID || !sessionKey || !sessionName) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      let changed = false;
      if (cached && cached.name !== sessionName) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          name: sessionName,
          updated_at: item?.updated_at || cached.updated_at,
        } as Session;
        changed = true;
      }

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        if (prev.name === sessionName) return prev;
        return {
          ...(prev as any),
          name: sessionName,
          updated_at: item?.updated_at || prev.updated_at,
        } as SessionItem;
      });

      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer?.key === sessionKey && drawer.name !== sessionName) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          name: sessionName,
          updated_at: item?.updated_at || drawer.updated_at,
        } as Session);
        changed = true;
      }

      if (changed) {
        bumpCacheVersion();
      }
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );
  const loadSessionsForRoot = useCallback(
    async (
      rootID: string,
      options?: {
        beforeTime?: string;
        afterTime?: string;
        replace?: boolean;
        force?: boolean;
      },
    ) => {
      const _nid = getNodeIdForRoot(rootID);
      const snapNode = String(_nid || "").trim();
      const snapRoot = String(rootID || "").trim();
      const seq = ++sessionListLoadSeqRef.current;
      sessionListAbortRef.current?.abort();
      const controller = new AbortController();
      sessionListAbortRef.current = controller;
      try {
        const shouldReplace = options?.replace || (!options?.beforeTime && !options?.afterTime);
        if (shouldReplace) {
          const cached = await getCachedSessionList(rootID, _nid);
          if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
          if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
          if (cached && (options?.force || currentRootIdRef.current === rootID)) {
            const cachedItems = [...cached.items, ...cached.pinnedItems]
              .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode }))
              .filter((item): item is SessionItem => !!item);
            setHasMoreSessions(cached.totalCount > cached.items.length);
            setSessions(
              applyPinSnapshot(
                mergeSessionItems([], cachedItems),
                rootID,
              ),
            );
          }
        }
        const payload = await sessionService.fetchSessions(rootID, {
          nodeId: _nid,
          beforeTime: options?.beforeTime,
          afterTime: options?.afterTime,
        });
        if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
        if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
        if (String(currentRootIdRef.current || "").trim() !== snapRoot && !options?.force) { return; }
        const next = [
          ...payload.items,
          ...payload.pinnedItems,
        ].map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode })).filter((item): item is SessionItem => !!item);
        // D3: 会话打标，便于切节点过滤与 key 隔离
        if (!options?.force && currentRootIdRef.current !== rootID) return;
        // 迟到覆盖已在 seq/snap 校验后再次用 currentRootId 守卫
        if (!options?.force && String(currentRootIdRef.current || "").trim() !== snapRoot) return;
        setHasMoreSessions(payload.totalCount > payload.items.length);
        if (shouldReplace) {
          // 服务端列表接口不含 context_window，全量重拉会把它清掉（WS message_done 已同步进本地列表）。
          // 用 setSessions 回调继承旧列表的 context_window，避免徽标闪没。
          setSessions((prev) => {
            const prevContext = new Map(
              prev
                .filter((item) => item.context_window)
                .map((item) => [
                  String(item.key || item.session_key || ""),
                  item.context_window,
                ]),
            );
            const merged = mergeSessionItems([], next).map((item) => {
              const itemKey = String(item.key || item.session_key || "");
              const inherited = prevContext.get(itemKey);
              return inherited
                ? { ...item, context_window: inherited }
                : item;
            });
            return applyPinSnapshot(merged, rootID);
          });
          void saveCachedSessionList(rootID, payload, _nid);
          return;
        }
        setSessions((prev) =>
          applyPinSnapshot(mergeSessionItems(prev, next), rootID),
        );
      } catch (err) {
        if ((err as any)?.name === "AbortError") return;
        if (controller.signal.aborted) return;
        // 列表拉取失败不再静默：WS 抖动时列表会停留旧状态（2026-09-09 排查），
        // 可见告警让用户知道需要手动同步；10s 冷却防断网期间刷屏。
        if (Date.now() - listLoadErrorAtRef.current > 10000) {
          listLoadErrorAtRef.current = Date.now();
          reportError(
            "session.list_load_failed",
            String((err as Error)?.message || err),
            { severity: "warning", recoverable: true },
          );
        }
      }
    },
    [getNodeIdForRoot],
  );
  const loadChildSessionsForParent = useCallback(
    async (
      parent: SessionItem,
      options?: { beforeTime?: string },
    ): Promise<{ hasMore: boolean }> => {
      const rootID =
        (parent.root_id as string | undefined) || currentRootIdRef.current || "";
      const parentKey = parent.key || parent.session_key || "";
      if (!rootID || !parentKey) {
        return { hasMore: false };
      }
      const childNodeId = (parent as any)?._nodeId || getNodeIdForRoot(rootID);
      const items = await sessionService.fetchChildSessions(rootID, parentKey, {
        beforeTime: options?.beforeTime,
        limit: CHILD_SESSION_PAGE_SIZE,
        nodeId: childNodeId,
      });
      // 响应体不带 _nodeId（那是前端传输级打标），必须在这里补：mergeSessionItems
      // 按 `_nodeId::key` 归并，漏了会让这批子会话归到 `::key` 而既有条目是 `local::key`，
      // 并存成一整批重复的 React key；重复 key 会让收起子会话时留下孤儿 DOM
      // （实测展开 131 行只收到 55 行，且这批重复永久留在分组里）。
      const next = items
        .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: childNodeId }))
        .filter((item): item is SessionItem => !!item);
      if (currentRootIdRef.current === rootID && next.length > 0) {
        setSessions((prev) => mergeSessionItems(prev, next));
      }
      if (next.length > 0) {
        setMultiProjectSessionGroups((prev) =>
          applyPendingToMultiProjectGroups(
            prev.map((group) =>
              group.rootId === rootID && (group as any)._nodeId === (parent as any)?._nodeId
                ? { ...group, sessions: mergeSessionItems(group.sessions, next) }
                : group,
            ),
            multiProjectPendingRef.current,
          ),
        );
      }
      return { hasMore: items.length >= CHILD_SESSION_PAGE_SIZE };
    },
    [applyPendingToMultiProjectGroups, mergeSessionItems],
  );
  const handleSelectSession = useCallback(
    async (
      session: any,
      options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean },
    ) => {
      const key = session?.key || session?.session_key;
      const targetRoot =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!targetRoot || !key) return;
      if (!options?.preserveTaskSelection) {
        setSelectedKanbanTaskId("");
      }
      // 多节点同名项目：会话归属节点优先于当前选中节点（跨节点切换会清理旧会话态）
      const sessionNode = String((session as any)?._nodeId || "").trim();
      const nextNode =
        sessionNode ||
        (targetRoot === String(currentRootIdRef.current || "")
          ? String(currentRootNodeIdRef.current || "")
          : "") ||
        getNodeIdForRoot(targetRoot) ||
        "";
      if (
        targetRoot !== String(currentRootIdRef.current || "") ||
        (sessionNode && sessionNode !== String(currentRootNodeIdRef.current || ""))
      ) {
        setCurrentRootId(targetRoot);
        selectRootNode(targetRoot, nextNode || undefined);
      }
      setBoundSessionForRoot(targetRoot, key);
      setDrawerSessionForRoot(targetRoot, toSessionItem(targetRoot, session));
      setSelectedDir(targetRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(targetRoot), targetRoot, targetRoot, true),
      );
      // 深链恢复时主面板模式以 URL 的 view 为准，不因为「有 session」就把面板硬拉回对话态。
      if (!options?.preserveMainView) {
        switchMainView("chat");
      }
      const currentDrawer = drawerSessionByRootRef.current[scopedRootKey(targetRoot)];
      const preservePending =
        currentDrawer?.key === key
          ? !!(currentDrawer as any)?.pending
          : !!(session as any)?.pending;
      const searchTargetId =
        typeof session?.search_seq === "number"
          ? `${key}:${session.search_seq}:${++sessionSearchTargetCounterRef.current}`
          : undefined;
      replaceURLState({
        root: targetRoot,
        file: "",
        session: key,
        cursor: 0,
        pluginQuery: {},
      });
      selectedSessionByRootRef.current[scopedRootKey(targetRoot)] = key;
      const cacheKey = rootSessionKey(targetRoot, key);
      setSelectedSessionLoading(true);
      setSelectedSession(
        toSessionItem(targetRoot, {
          ...(session as any),
          pending: preservePending,
          search_target_id: searchTargetId || session?.search_target_id,
        }),
      );
      setInteractionMode("main");
      setDrawerOpenForRoot(targetRoot, false);
      if (isMobile) setIsRightOpen(false);
      await waitForNextPaint();
      const applySession = (
        fullSession: Session,
        options?: { writeCache?: boolean },
      ) => {
        const shouldWriteCache = options?.writeCache !== false;
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        const pending =
          serverPending !== undefined
            ? serverPending
            : resolvePendingForSession(targetRoot, key, preservePending);
        const normalized = {
          ...(fullSession as any),
          key,
          // CUSTOM(G-Z): 所有走 handleSelectSession 的路径写缓存都带 root，
          // 这是「无主缓存」的收口 —— 缺了它，二次跳转会把它当成当前根的会话。
          root_id: targetRoot,
          pending,
        } as Session;
        if (shouldWriteCache) {
          sessionCacheRef.current[cacheKey] = normalized;
        }
        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (prevKey !== key || prevRoot !== targetRoot) {
            return prev;
          }
          return toSessionItem(targetRoot, {
            ...(prev as any),
            ...(normalized as any),
            key,
            session_key: key,
            root_id: targetRoot,
          });
        });
        if ((boundSessionByRootRef.current[scopedRootKey(targetRoot)] || null) === key) {
          setDrawerSessionForRoot(targetRoot, {
            ...(normalized as any),
          } as Session);
        }
        setSelectedSessionLoading(false);
        if (shouldWriteCache) {
          bumpCacheVersion();
        }
      };
      // 先即时渲染本地缓存（快），再始终同步到最新：
      // 移除原"已缓存/已加载就跳过 sync"的短路，避免陈旧缓存导致历史加载/更新滞后（#sync-lag）。
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        applySession(cached);
      } else {
        const persisted = await getCachedSession(targetRoot, key, getNodeIdForRoot(targetRoot));
        if (persisted) {
          applySession(persisted);
        }
      }
      try {
        const restored = await restoreActiveSession(targetRoot, key);
        if (restored) {
          applySession(restored, { writeCache: false });
          loadedSessionRef.current[cacheKey] = true;
          clearSessionStale(targetRoot, key);
        } else {
          setSelectedSessionLoading(false);
        }
      } catch (err) {
        setSelectedSessionLoading(false);
      }
    },
    [
      isMobile,
      rootSessionKey,
      bumpCacheVersion,
      clearSessionStale,
      isSessionStale,
      resolvePendingForSession,
      restoreActiveSession,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      switchMainView,
      replaceURLState,
    ],
  );
  const tryShowBoundSessionForRoot = useCallback(
    async (
      rootID: string | null | undefined,
      options?: {
        pluginQuery?: Record<string, string>;
        closeLeftSidebar?: boolean;
      },
    ): Promise<boolean> => {
      const resolvedRoot = String(rootID || "");
      if (!resolvedRoot) {
        return false;
      }
      if (mainViewRef.current !== "chat") {
        return false;
      }
      const selectedKey = String(
        selectedSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const boundKey = String(
        boundSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const drawerKey = String(
        drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)]?.key || "",
      ).trim();
      const preferredKey =
        (selectedKey && !selectedKey.startsWith("pending-") ? selectedKey : "") ||
        (boundKey && !boundKey.startsWith("pending-") ? boundKey : "") ||
        (drawerKey && !drawerKey.startsWith("pending-") ? drawerKey : "");
      if (!preferredKey) {
        return false;
      }
      if (currentRootIdRef.current !== resolvedRoot) {
        setCurrentRootId(resolvedRoot);
      }
      setGitDiff(null);
      setFile(null);
      setMainEntries([]);
      setPluginQuery(options?.pluginQuery || {});
      setSelectedDir(resolvedRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(resolvedRoot), resolvedRoot, resolvedRoot, true),
      );
      fileCursorRef.current = 0;
      const cacheKey = rootSessionKey(resolvedRoot, preferredKey);
      let initialSession =
        sessionCacheRef.current[cacheKey] ||
        (await getCachedSession(resolvedRoot, preferredKey, getNodeIdForRoot(resolvedRoot)));
      if (initialSession) {
        sessionCacheRef.current[cacheKey] = {
          ...(initialSession as any),
          key: preferredKey,
        } as Session;
      }
      await handleSelectSession(
        initialSession
          ? ({
              ...(initialSession as any),
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            } as SessionItem)
          : {
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            },
      );
      if (options?.closeLeftSidebar && isMobile) {
        setIsLeftOpen(false);
      }
      return true;
    },
    [handleSelectSession, isMobile, rootSessionKey, bumpCacheVersion],
  );
  const handleRenameSession = useCallback(
    async (session: SessionItem, nextName: string) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;

      const trimmedName = nextName.trim();
      if (!trimmedName || trimmedName === String(session?.name || "").trim()) {
        return true;
      }

      const renamed = await sessionService.renameSession(
        rootID,
        sessionKey,
        trimmedName,
        String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID),
      );
      if (!renamed) {
        reportError("session.rename_failed", t("session.renameFailed"));
        return false;
      }

      setSessions((prev) =>
        prev.map((item) =>
          (item.key || item.session_key) === sessionKey
            ? ({
                ...item,
                name: renamed.name,
                updated_at: renamed.updated_at,
              } as SessionItem)
            : item,
        ),
      );

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...cached,
          name: renamed.name,
          updated_at: renamed.updated_at,
        } as Session;
      }

      if (
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
      ) {
        setSelectedSession((prev) =>
          prev
            ? ({
                ...prev,
                name: renamed.name,
                updated_at: renamed.updated_at,
              } as SessionItem)
            : prev,
        );
      }

      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        const latest = sessionCacheRef.current[cacheKey];
        if (latest) {
          setDrawerSessionForRoot(rootID, latest);
        }
      }
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) =>
          group.rootId === rootID && (!(session as any)?._nodeId || (group as any)._nodeId === (session as any)?._nodeId)
            ? {
                ...group,
                sessions: group.sessions.map((item) =>
                  (item.key || item.session_key) === sessionKey
                    ? ({
                        ...item,
                        name: renamed.name,
                        updated_at: renamed.updated_at,
                      } as SessionItem)
                    : item,
                ),
              }
            : group,
        ),
      );

      bumpCacheVersion();
      return true;
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot],
  );
  const handleSyncSession = useCallback(
    async (session: SessionItem) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey || sessionKey.startsWith("pending-")) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      setSyncingSessionKeys((prev) => {
        const next = new Set(prev);
        next.add(cacheKey);
        next.add(sessionKey);
        return next;
      });
      try {
        const result = await syncSession(rootID, sessionKey, { full: true, nodeId: String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID) });
        const synced = result.session;
        if (!synced) {
          reportError("session.sync_failed", t("session.syncFailed"));
          return;
        }
        // 点「同步」必须保住「正在等待回答的 ask_user 卡」。
        // 该卡是纯瞬时态：只存在于前端内存缓存（WS tool_call 追加的 seq=0 role=tool 条目）
        // 与服务端内存（manager.pendingToolCalls）里，两边都不会经 HTTP 下发
        // （session.Exchange 结构体没有 ToolCall 字段）。syncSession 以 IDB 缓存为 base，
        // 而 IDB 里从来没有这些瞬时条目，于是同步后卡片凭空消失（实测 2026-09-12 症状 2）。
        // 这里从内存缓存按 callId 去重后把它们合并回来；落盘后同一 callId 会出现在窗口 aux
        // 中，SessionViewer 的 overlay 对账（windowToolCallIds）会自然让位，不会重复渲染。
        // 同步会把整个会话的持久化交换灌回来（实测缓存 24 → 92），但**本轮尚未落盘的内容**
        // 只存在于内存缓存的 seq=0 瞬时条目里。原先这里只保留 role=tool 一类，于是
        // 「点同步的瞬间，正在流式输出的正文凭空消失，而 ask 卡还在」——2026-09-13 实测
        // cacheBeforeTransient=4 只保住 1 条（keptToolTransient=1），丢掉的 3 条正是非 tool
        // 的直播正文。
        // 现在两类都保留，各按「同步结果里是否已有」去重：
        //   · tool 条目按 callId（落盘后同一 callId 会出现在窗口 aux，SessionViewer 的
        //     overlay 对账 windowToolCallIds 会自然让位，不会重复渲染）
        //   · 其余条目按 role+内容（已持久化的轮次不会以 seq=0 形式被重复追加，这正是当初
        //     只留 tool 想防的事；用内容比对同样防得住）
        //
        // **为什么这里不用 composeLoadedExchanges**：那条规则是「服务端回包只含已落盘行，
        // 所以本地瞬时行无脑全留」。手动同步不同 —— 它把整个会话灌回来，刚落盘的那一轮
        // 已经以**真实 seq** 出现在回包里，此时本地那几份 seq=0 的拷贝就成了陈旧副本，
        // 再留就是同一段正文显示两遍。所以这条路径必须比对后剔除，判据也更强（callId 或
        // role+内容）。两条规则形状不同是**有意的**，不是漏统一。
        const localTransientTail = (() => {
          const cachedExchanges = Array.isArray(
            (sessionCacheRef.current[cacheKey] as any)?.exchanges,
          )
            ? ((sessionCacheRef.current[cacheKey] as any).exchanges as any[])
            : [];
          const present = new Set<string>();
          const presentText = new Set<string>();
          const syncedExchanges = Array.isArray((synced as any)?.exchanges)
            ? ((synced as any).exchanges as any[])
            : [];
          for (const ex of syncedExchanges) {
            const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
            if (callId) present.add(callId);
            presentText.add(
              `${String((ex as any)?.role || "").toLowerCase()}|${String(
                (ex as any)?.content || "",
              )}`,
            );
          }
          const syncedAux = ((synced as any)?.exchange_aux || {}) as Record<
            string,
            any[]
          >;
          for (const items of Object.values(syncedAux)) {
            for (const aux of items || []) {
              const callId = `${(aux as any)?.toolcall?.callId || ""}`.trim();
              if (callId) present.add(callId);
            }
          }
          return cachedExchanges.filter((ex) => {
            if (!isTransientExchange(ex)) return false;
            const role = String((ex as any)?.role || "").toLowerCase();
            if (role === "tool") {
              const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
              return !callId || !present.has(callId);
            }
            const content = String((ex as any)?.content || "");
            if (!content) return false;
            return !presentText.has(`${role}|${content}`);
          });
        })();
        const syncedExchanges = Array.isArray((synced as any)?.exchanges)
          ? ((synced as any).exchanges as any[])
          : [];
        const normalized = {
          ...(synced as any),
          key: sessionKey,
          exchanges: [...syncedExchanges, ...localTransientTail],
        } as Session;
        sessionCacheRef.current[cacheKey] = normalized;
        loadedSessionRef.current[cacheKey] = true;
        clearSessionStale(rootID, sessionKey);

        const nextItem = toSessionItem(rootID, normalized);
        if (nextItem) {
          setSessions((prev) => mergeSessionItems(prev, [nextItem]));
        }

        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
            return prev;
          }
          return toSessionItem(rootID, {
            ...(prev as any),
            ...(normalized as any),
            key: sessionKey,
            session_key: sessionKey,
            root_id: rootID,
          });
        });

        if (drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key === sessionKey) {
          setDrawerSessionForRoot(rootID, normalized);
        }

        bumpCacheVersion();
      } catch {
        reportError("session.sync_failed", t("session.syncFailed"));
      } finally {
        setSyncingSessionKeys((prev) => {
          const next = new Set(prev);
          next.delete(cacheKey);
          next.delete(sessionKey);
          return next;
        });
      }
    },
    [
      bumpCacheVersion,
      clearSessionStale,
      mergeSessionItems,
      rootSessionKey,
      setDrawerSessionForRoot,
    ],
  );

  return {
    restoreActiveSession,
    syncSessionHeaderFromListItem,
    loadSessionsForRoot,
    loadChildSessionsForParent,
    handleSelectSession,
    tryShowBoundSessionForRoot,
    handleRenameSession,
    handleSyncSession,
  };
}
