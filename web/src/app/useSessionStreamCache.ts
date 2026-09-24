import { useCallback, useRef } from "react";
import type { Session } from "../services/session";
import type { Exchange } from "./appSession";
import { normalizeFastService } from "./appTask";

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

type SessionRuntimeMeta = {
  agent?: string;
  model?: string;
  model_display_name?: string;
  mode?: string;
  effort?: string;
  fast_service?: "" | "on" | "off";
};

export type SessionStreamCacheParams = {
  sessionCacheRef: React.MutableRefObject<Record<string, Session>>;
  /** App 提供：把 (rootId, sessionKey) 解析成带节点作用域的缓存键。 */
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  /** 防抖 bump（App 已用 useCallback([]) 稳定身份）。 */
  bumpCacheVersionDebounced: () => void;
  currentSessionRef: React.MutableRefObject<any>;
  selectedSessionRef: React.MutableRefObject<any>;
};

export function useSessionStreamCache({
  sessionCacheRef,
  rootSessionKey,
  bumpCacheVersionDebounced,
  currentSessionRef,
  selectedSessionRef,
}: SessionStreamCacheParams) {
  // bump 收进 ref：即使调用方换了新函数身份，下面这些 useCallback 的依赖也不变，
  // 6 个 append 不会每渲染重建（否则 WS effect 的依赖数组会跟着抖）。
  const bumpRef = useRef(bumpCacheVersionDebounced);
  bumpRef.current = bumpCacheVersionDebounced;

  /**
   * 共享外壳：读缓存 → 缺失则造 stub → 交给 updateList 改 exchanges → 写回 → bump。
   * updateList 必须「返回新数组」，与原文各分支的写法保持一致。
   */
  const upsertSessionCache = useCallback(
    (
      rootID: string,
      sessionKey: string,
      updateList: (prevList: Exchange[]) => Exchange[],
      meta?: SessionRuntimeMeta,
    ): void => {
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const base =
        sessionCacheRef.current[cacheKey] ||
        ({
          key: sessionKey,
          type: "chat",
          agent: meta?.agent || "",
          model: meta?.model,
          mode: meta?.mode,
          effort: meta?.effort,
          fast_service: meta?.fast_service,
          name: "",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
          exchanges: [],
        } as any);
      const nextList = updateList(((base as any).exchanges || []) as Exchange[]);
      const next: Record<string, unknown> = {
        ...(base as any),
        exchanges: nextList,
        updated_at: new Date().toISOString(),
      };
      // 元信息「新值优先、旧值兜底」——只有带 meta 的调用方（appendAgentChunk）会走到这里。
      if (meta) {
        for (const field of [
          "agent",
          "model",
          "model_display_name",
          "mode",
          "effort",
          "fast_service",
        ] as const) {
          const value = meta[field];
          if (value) next[field] = value;
        }
      }
      sessionCacheRef.current[cacheKey] = next as Session;
      bumpRef.current();
    },
    [rootSessionKey, sessionCacheRef],
  );

  const resolveRuntimeMetaForSession = useCallback(
    (
      rootID: string,
      sessionKey: string,
      fallback?: SessionRuntimeMeta,
    ) => {
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cachedSession = sessionCacheRef.current[cacheKey] as any;
      const exchanges = Array.isArray(cachedSession?.exchanges)
        ? ((cachedSession.exchanges || []) as Exchange[])
        : [];
      const latestMatchingExchange = [...exchanges]
        .reverse()
        .find(
          (item) =>
            item?.agent ||
	            item?.model ||
	            item?.mode ||
	            item?.effort ||
	            item?.fast_service,
        );
      const candidates = [
        fallback,
        latestMatchingExchange as any,
        sessionCacheRef.current[cacheKey] as any,
        currentSessionRef.current?.key === sessionKey
          ? (currentSessionRef.current as any)
          : null,
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
          ? (selectedSessionRef.current as any)
          : null,
      ];

      const pickText = (field: "agent" | "model" | "model_display_name" | "mode" | "effort") => {
        for (const item of candidates) {
          const value = `${item?.[field] || ""}`.trim();
          if (value) return value;
        }
        return "";
      };
      const pickFastService = (): "" | "on" | "off" => {
        for (const item of candidates) {
          const value = normalizeFastService(item?.fast_service);
          if (value) return value;
        }
        return "";
      };
	      return {
	        agent: pickText("agent"),
	        model: pickText("model"),
	        model_display_name: pickText("model_display_name"),
	        mode: pickText("mode"),
	        effort: pickText("effort"),
	        fast_service: pickFastService(),
	      };
    },
    [rootSessionKey, sessionCacheRef, currentSessionRef, selectedSessionRef],
  );

  const appendAgentChunkForSession = useCallback(
    (rootID: string, sessionKey: string, content: string, runtimeHint?: SessionRuntimeMeta): void => {
    if (!content) return;
    const now = new Date().toISOString();
    const runtimeMeta = resolveRuntimeMetaForSession(rootID, sessionKey, runtimeHint);
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (last && (last.role === "agent" || last.role === "assistant")) {
        list[list.length - 1] = {
          ...last,
          agent: runtimeMeta.agent || last.agent,
          model: runtimeMeta.model || last.model,
          model_display_name:
            runtimeMeta.model_display_name || last.model_display_name,
            mode: runtimeMeta.mode || last.mode,
            effort: runtimeMeta.effort || last.effort,
            fast_service: runtimeMeta.fast_service || last.fast_service,
            content: `${last.content || ""}${content}`,
          timestamp: now,
        };
        return list;
      }
      list.push({
        role: "agent",
        agent: runtimeMeta.agent,
        model: runtimeMeta.model,
        model_display_name: runtimeMeta.model_display_name,
          mode: runtimeMeta.mode,
          effort: runtimeMeta.effort,
          fast_service: runtimeMeta.fast_service,
          content,
        timestamp: now,
      });
      return list;
    });
  }, [upsertSessionCache, resolveRuntimeMetaForSession]);

  const appendThoughtChunkForSession = useCallback(
    (rootID: string, sessionKey: string, content: string, thoughtID?: string): void => {
    if (!content) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      if (thoughtID) {
        const existingIndex = list.findIndex(
          (item) => item.role === "thought" && item.thought_id === thoughtID,
        );
        if (existingIndex >= 0) {
          const existing = list[existingIndex];
          const existingContent = existing.content || "";
          let nextContent = existingContent;
          if (content.includes(existingContent)) {
            nextContent = content;
          } else if (!existingContent.includes(content)) {
            nextContent = `${existingContent}${content}`;
          }
          list[existingIndex] = {
            ...existing,
            content: nextContent,
            timestamp: now,
          };
          return list;
        }
      }
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (last && last.role === "thought" && (!thoughtID || !last.thought_id)) {
        list[list.length - 1] = {
          ...last,
          content: `${last.content || ""}${content}`,
          thought_id: thoughtID || last.thought_id,
          timestamp: now,
        };
        return list;
      }
      list.push({ role: "thought", content, thought_id: thoughtID, timestamp: now });
      return list;
    });
  }, [upsertSessionCache]);

  const appendToolCallForSession = useCallback(
    (rootID: string, sessionKey: string, toolCall: any, update: boolean): void => {
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
            (list[i]?.toolCall?.callId === callId ||
              list[i]?.toolCall?.toolCallId === callId ||
              list[i]?.toolCall?.tool_call_id === callId)
          ) {
            list[i] = {
              ...list[i],
              timestamp: now,
              toolCall: mergeToolCall(list[i].toolCall, toolCall),
            };
            return list;
          }
        }
      }
      list.push({ role: "tool", content: "", timestamp: now, toolCall });
      return list;
    });
  }, [upsertSessionCache]);

  const appendTodoUpdateForSession = useCallback(
    (rootID: string, sessionKey: string, todoUpdate: any): void => {
    if (!todoUpdate) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      for (let i = list.length - 1; i >= 0; i -= 1) {
        if (list[i]?.role !== "todo") continue;
        list[i] = {
          ...list[i],
          timestamp: now,
          todoUpdate,
        };
        return list;
      }
      list.push({ role: "todo", content: "", timestamp: now, todoUpdate });
      return list;
    });
  }, [upsertSessionCache]);

  const appendPlanUpdateForSession = useCallback(
    (rootID: string, sessionKey: string, planUpdate: any): void => {
    if (!planUpdate) return;
    const content = `${planUpdate.content || ""}`;
    if (!content) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const planId = `${planUpdate.id || ""}`;
      if (planId) {
        for (let i = list.length - 1; i >= 0; i -= 1) {
          if (list[i]?.role !== "plan") continue;
          if (`${list[i]?.planUpdate?.id || ""}` !== planId) continue;
          const existingContent = `${list[i].planUpdate?.content || ""}`;
          const nextContent = planUpdate.delta
            ? `${existingContent}${content}`
            : content;
          list[i] = {
            ...list[i],
            timestamp: now,
            planUpdate: { ...list[i].planUpdate, ...planUpdate, content: nextContent },
          };
          return list;
        }
      }
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (last?.role === "plan" && !planId) {
        const existingContent = `${last.planUpdate?.content || ""}`;
        list[list.length - 1] = {
          ...last,
          timestamp: now,
          planUpdate: {
            ...last.planUpdate,
            ...planUpdate,
            content: planUpdate.delta ? `${existingContent}${content}` : content,
          },
        };
        return list;
      }
      list.push({ role: "plan", content: "", timestamp: now, planUpdate });
      return list;
    });
  }, [upsertSessionCache]);

  const appendCompactNoticeForSession = useCallback(
    (rootID: string, sessionKey: string, compactNotice: any): void => {
    if (!compactNotice) return;
    const now = new Date().toISOString();
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const compactId = `${compactNotice.id || ""}`;
      if (compactId) {
        for (let i = list.length - 1; i >= 0; i -= 1) {
          if (list[i]?.role !== "compact") continue;
          if (`${list[i]?.compactNotice?.id || ""}` !== compactId) continue;
          list[i] = {
            ...list[i],
            timestamp: now,
            compactNotice: { ...list[i].compactNotice, ...compactNotice },
          };
          return list;
        }
      }
      list.push({ role: "compact", content: "", timestamp: now, compactNotice });
      return list;
    });
  }, [upsertSessionCache]);

  return {
    upsertSessionCache,
    resolveRuntimeMetaForSession,
    appendAgentChunkForSession,
    appendThoughtChunkForSession,
    appendToolCallForSession,
    appendTodoUpdateForSession,
    appendPlanUpdateForSession,
    appendCompactNoticeForSession,
  };
}
