import { useCallback } from "react";
import type { Session, TokenUsage } from "../services/session";
import type { Exchange, SessionItem } from "./appSession";

/**
 * 会话流写缓存内核。
 *
 * 6 个 `append*ForSession` 原本各自抄一遍同一段外壳：「读缓存 → 缺则造 stub → 改 exchanges
 * → 写回 → 防抖 bump」。这里只把外壳收成 `upsertSessionCache`，**每个 role 的 merge 逻辑
 * 原样留在各自函数里**——thought 按 thought_id 去重、tool 按 callId 合并并挡住状态回退、
 * plan 按 plan id 累加……规则各不相同，硬塞进一张策略映射表只会比原来更抽象。
 *
 * 关于"造 stub"：原文里只有 appendAgentChunk 的 stub 带 agent/model 等元信息，其余都留空。
 * upsertSessionCache 统一成「有 meta 就写进 stub」——对 agent 分支结果相同，对其他分支
 * meta 为 undefined，行为不变。
 */

export type { SessionRuntimeMeta } from "./sessionStreamCacheTypes";
import { useSessionStreamRuntimeMeta } from "./useSessionStreamRuntimeMeta";

/**
 * G-BI：实时行按 MindFS 身份对账。只有「同一 mindfs_id」的行才允许拼接/就地更新 ——
 * 相邻两轮同 role 的行（都是 agent 正文）此前会被无条件拼成一行。无 mindfs_id
 * 的旧帧/流式项仍照原样拼接（事件只会投递一次，snapshot reset 时先清瞬时尾巴）。
 */
const hasSameMindFSID = (item: any, mindfsID?: string): boolean =>
  String(item?.mindfs_id || "").trim() === String(mindfsID || "").trim();

export type SessionStreamCacheParams = {
  sessionCacheRef: React.MutableRefObject<Record<string, Session>>;
  /** App 提供：把 (rootId, sessionKey) 解析成带节点作用域的缓存键。 */
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  /** 防抖 bump（App 已用 useCallback([]) 稳定身份）。 */
  bumpCacheVersionDebounced: () => void;
  currentSessionRef: React.MutableRefObject<any>;
  selectedSessionRef: React.MutableRefObject<any>;
  /** 立即 bump（attachContextWindow 用；它没有 append 那样的高频调用，不需要防抖）。 */
  bumpCacheVersion: () => void;
  currentRootIdRef: React.MutableRefObject<string | null>;
  drawerSessionByRootRef: React.MutableRefObject<Record<string, any>>;
  scopedRootKey: (rootId: string) => string;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: any) => void;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
};

export function useSessionStreamCache({
  sessionCacheRef,
  rootSessionKey,
  bumpCacheVersionDebounced,
  currentSessionRef,
  selectedSessionRef,
  bumpCacheVersion,
  currentRootIdRef,
  drawerSessionByRootRef,
  scopedRootKey,
  setDrawerSessionForRoot,
  setSelectedSession,
  setSessions,
}: SessionStreamCacheParams) {
  const {
    upsertSessionCache,
    resolveRuntimeMetaForSession,
    appendAgentChunkForSession,
  } = useSessionStreamRuntimeMeta({
    sessionCacheRef,
    rootSessionKey,
    bumpCacheVersionDebounced,
    currentSessionRef,
    selectedSessionRef,
  });

  const appendThoughtChunkForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      content: string,
      thoughtID?: string,
      mindfsID?: string,
    ): void => {
    if (!content) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      if (thoughtID) {
        const existingIndex = list.findIndex(
          (item) =>
            item.role === "thought" &&
            item.thought_id === thoughtID &&
            hasSameMindFSID(item, mindfsID),
        );
        if (existingIndex >= 0) {
          const existing = list[existingIndex];
          list[existingIndex] = {
            ...existing,
            ...(mindfsID ? { mindfs_id: mindfsID } : {}),
            content: String(existing.content || "") + String(content || ""),
            timestamp: now,
          };
          return list;
        }
      }
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (
        last &&
        last.role === "thought" &&
        (!thoughtID || !last.thought_id) &&
        hasSameMindFSID(last, mindfsID)
      ) {
        list[list.length - 1] = {
          ...last,
          ...(mindfsID ? { mindfs_id: mindfsID } : {}),
          content: String(last.content || "") + String(content || ""),
          thought_id: thoughtID || last.thought_id,
          timestamp: now,
        };
        return list;
      }
      list.push({
        role: "thought",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
        content,
        thought_id: thoughtID,
        timestamp: now,
      });
      return list;
    });
  }, [upsertSessionCache]);

  const appendToolCallForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      toolCall: any,
      update: boolean,
      mindfsID?: string,
    ): void => {
    if (!toolCall) return;
    const now = new Date().toISOString();
    const mergeToolCall = (existing: any, incoming: any) => {
      const merged = { ...(existing || {}), ...incoming };
      const incomingMeta = (incoming?.meta || {}) as Record<string, unknown>;
      if (existing?.meta || incoming?.meta) {
        merged.meta = { ...(existing?.meta || {}), ...incomingMeta };
      }
      const isUserShellStream =
        incomingMeta.source === "userShell" && incomingMeta.phase === "stream";
      if (isUserShellStream) {
        // replaySnapshot 时后端已做全量覆盖，前端不再与旧 text 叠加。
        if (incomingMeta.replaySnapshot === true) {
          const incomingText = (incoming?.content || [])
            .map((item: any) => item?.text || "")
            .join("");
          if (incomingText.length > 256 * 1024) {
            merged.content = [{ type: "text", text: incomingText.slice(-256 * 1024) }];
          } else {
            merged.content = incomingText ? [{ type: "text", text: incomingText }] : [];
          }
          merged.meta = { ...(existing?.meta || {}), ...incomingMeta };
        } else {
          const existingText = (existing?.content || [])
            .map((item: any) => item?.text || "")
            .join("");
          const incomingText = (incoming?.content || [])
            .map((item: any) => item?.text || "")
            .join("");
          const totalText = existingText + incomingText;
        if (totalText.length > 256 * 1024) {
          merged.content = [{ type: "text", text: totalText.slice(-256 * 1024) }];
        } else {
          merged.content = totalText ? [{ type: "text", text: totalText }] : [];
          }
          merged.meta = { ...(existing?.meta || {}), ...incomingMeta };
        }
      }
      if (!incoming.kind && existing?.kind) merged.kind = existing.kind;
      if (!incoming.title && existing?.title) merged.title = existing.title;
      const existingStatus = `${existing?.status || ""}`.toLowerCase();
      const incomingStatus = `${incoming?.status || ""}`.toLowerCase();
      if (
        (existingStatus === "failed" ||
          existingStatus === "error" ||
          existingStatus === "complete" ||
          existingStatus === "success") &&
        (incomingStatus === "running" ||
          incomingStatus === "pending" ||
          incomingStatus === "in_progress")
      ) {
        merged.status = existing.status;
      }
      return merged;
    };
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const callId =
        toolCall.callId || toolCall.toolCallId || toolCall.tool_call_id || "";
      if (callId) {
        for (let i = list.length - 1; i >= 0; i--) {
          if (
            list[i]?.role === "tool" &&
            hasSameMindFSID(list[i], mindfsID) &&
            (list[i]?.toolCall?.callId === callId ||
              list[i]?.toolCall?.toolCallId === callId ||
              list[i]?.toolCall?.tool_call_id === callId)
          ) {
            list[i] = {
              ...list[i],
              ...(mindfsID ? { mindfs_id: mindfsID } : {}),
              timestamp: now,
              toolCall: mergeToolCall(list[i].toolCall, toolCall),
            };
            return list;
          }
        }
      }
      list.push({
        role: "tool",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
        content: "",
        timestamp: now,
        toolCall,
      });
      return list;
    });
  }, [upsertSessionCache]);

  const appendTodoUpdateForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      todoUpdate: any,
      mindfsID?: string,
    ): void => {
    if (!todoUpdate) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      for (let i = list.length - 1; i >= 0; i -= 1) {
        if (list[i]?.role !== "todo" || !hasSameMindFSID(list[i], mindfsID)) continue;
        list[i] = {
          ...list[i],
          ...(mindfsID ? { mindfs_id: mindfsID } : {}),
          timestamp: now,
          todoUpdate,
        };
        return list;
      }
      list.push({
        role: "todo",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
        content: "",
        timestamp: now,
        todoUpdate,
      });
      return list;
    });
  }, [upsertSessionCache]);

  const appendPlanUpdateForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      planUpdate: any,
      mindfsID?: string,
    ): void => {
    if (!planUpdate) return;
    const content = `${planUpdate.content || ""}`;
    if (!content) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const planId = `${planUpdate.id || ""}`;
      if (planId) {
        for (let i = list.length - 1; i >= 0; i -= 1) {
          if (list[i]?.role !== "plan" || !hasSameMindFSID(list[i], mindfsID)) continue;
          if (`${list[i]?.planUpdate?.id || ""}` !== planId) continue;
          const existingContent = `${list[i].planUpdate?.content || ""}`;
          const nextContent = planUpdate.delta
            ? `${existingContent}${content}`
            : content;
          list[i] = {
            ...list[i],
            ...(mindfsID ? { mindfs_id: mindfsID } : {}),
            timestamp: now,
            planUpdate: { ...list[i].planUpdate, ...planUpdate, content: nextContent },
          };
          return list;
        }
      }
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (last?.role === "plan" && !planId && hasSameMindFSID(last, mindfsID)) {
        const existingContent = `${last.planUpdate?.content || ""}`;
        list[list.length - 1] = {
          ...last,
          ...(mindfsID ? { mindfs_id: mindfsID } : {}),
          timestamp: now,
          planUpdate: {
            ...last.planUpdate,
            ...planUpdate,
            content: planUpdate.delta ? `${existingContent}${content}` : content,
          },
        };
        return list;
      }
      list.push({
        role: "plan",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
        content: "",
        timestamp: now,
        planUpdate,
      });
      return list;
    });
  }, [upsertSessionCache]);

  const appendCompactNoticeForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      compactNotice: any,
      mindfsID?: string,
    ): void => {
    if (!compactNotice) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const compactId = `${compactNotice.id || ""}`;
      if (compactId) {
        for (let i = list.length - 1; i >= 0; i -= 1) {
          if (list[i]?.role !== "compact" || !hasSameMindFSID(list[i], mindfsID)) continue;
          if (`${list[i]?.compactNotice?.id || ""}` !== compactId) continue;
          list[i] = {
            ...list[i],
            ...(mindfsID ? { mindfs_id: mindfsID } : {}),
            timestamp: now,
            compactNotice: { ...list[i].compactNotice, ...compactNotice },
          };
          return list;
        }
      }
      list.push({
        role: "compact",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
        content: "",
        timestamp: now,
        compactNotice,
      });
      return list;
    });
  }, [upsertSessionCache]);

  const attachContextWindowToLatestAssistant = useCallback(
    (
      rootID: string,
      sessionKey: string,
      contextWindow?: { totalTokens?: number; modelContextWindow?: number },
      tokenUsage?: TokenUsage,
    ) => {
      const totalTokens = Math.max(0, Number(contextWindow?.totalTokens || 0));
      const modelContextWindow = Math.max(0, Number(contextWindow?.modelContextWindow || 0));
      const hasContextWindow = totalTokens > 0 && modelContextWindow > 0;
      const normalizedTokenUsage = tokenUsage
        ? {
            inputTokens: Math.max(0, Number(tokenUsage.inputTokens || 0)),
            outputTokens: Math.max(0, Number(tokenUsage.outputTokens || 0)),
            ...(Number.isFinite(tokenUsage.cacheReadTokens)
              ? { cacheReadTokens: Math.max(0, Number(tokenUsage.cacheReadTokens)) }
              : {}),
            ...(Number.isFinite(tokenUsage.cacheWriteTokens)
              ? { cacheWriteTokens: Math.max(0, Number(tokenUsage.cacheWriteTokens)) }
              : {}),
          }
        : undefined;
      if (!hasContextWindow && !normalizedTokenUsage) {
        return;
      }
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const stampList = (prevList: Exchange[]) => {
        const list = [...(prevList || [])];
        for (let i = list.length - 1; i >= 0; i -= 1) {
          const item = list[i];
          if (
            (item?.role === "agent" || item?.role === "assistant") &&
            String(item?.content || "").trim()
          ) {
            list[i] = {
              ...item,
              ...(hasContextWindow
                ? { context_window: { totalTokens, modelContextWindow } }
                : {}),
              ...(normalizedTokenUsage
                ? { token_usage: normalizedTokenUsage }
                : {}),
            };
            break;
          }
        }
        return list;
      };
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        const exchanges = stampList((((cached as any).exchanges || []) as Exchange[]));
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          exchanges,
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
          updated_at: new Date().toISOString(),
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevRoot !== rootID || (prev.key || prev.session_key) !== sessionKey) {
          return prev;
        }
        return {
          ...(prev as any),
          exchanges: stampList((((prev as any).exchanges || []) as Exchange[])),
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
        } as SessionItem;
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer && drawer.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          exchanges: stampList((((drawer as any).exchanges || []) as Exchange[])),
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
        } as Session);
      }
      // 列表 state 同步：message_done 直推的 context_window 只进了 cache/drawer/selected，
      // 会话列表项没有数据源（列表接口不含该字段），不补这里则徽标停在旧值，直到全量重拉。
      setSessions((prev) =>
        prev.map((item) => {
          const itemKey = item.key || item.session_key;
          if (
            itemKey !== sessionKey ||
            (item.root_id as string | undefined) !== rootID
          ) {
            return item;
          }
          return {
            ...item,
            context_window: {
              totalTokens,
              modelContextWindow,
            },
          };
        }),
      );
      bumpCacheVersion();
    },
    [bumpCacheVersion, rootSessionKey, setSessions],
  );

  return {
    upsertSessionCache,
    attachContextWindowToLatestAssistant,
    resolveRuntimeMetaForSession,
    appendAgentChunkForSession,
    appendThoughtChunkForSession,
    appendToolCallForSession,
    appendTodoUpdateForSession,
    appendPlanUpdateForSession,
    appendCompactNoticeForSession,
  };
}
