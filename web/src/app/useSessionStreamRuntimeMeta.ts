import { useCallback, useRef } from "react";
import type * as React from "react";
import type { Session } from "../services/session";
import type { Exchange } from "./appSession";
import { normalizeFastService } from "./appTask";
import { streamEventTimestamp } from "../services/session/sessionDuration";
import type { SessionRuntimeMeta } from "./sessionStreamCacheTypes";

const hasSameMindFSID = (item: any, mindfsID?: string): boolean =>
  String(item?.mindfs_id || "").trim() === String(mindfsID || "").trim();

export function useSessionStreamRuntimeMeta({
  sessionCacheRef,
  rootSessionKey,
  bumpCacheVersionDebounced,
  currentSessionRef,
  selectedSessionRef,
}: {
  sessionCacheRef: React.MutableRefObject<Record<string, Session>>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  bumpCacheVersionDebounced: () => void;
  currentSessionRef: React.MutableRefObject<any>;
  selectedSessionRef: React.MutableRefObject<any>;
}) {
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
    (
      rootID: string,
      sessionKey: string,
      content: string,
      runtimeHint?: SessionRuntimeMeta,
      eventTimestamp?: string,
      mindfsID?: string,
    ): void => {
    if (!content) return;
    const now = streamEventTimestamp({ timestamp: eventTimestamp }, new Date().toISOString());
    const runtimeMeta = resolveRuntimeMetaForSession(rootID, sessionKey, runtimeHint);
    upsertSessionCache(rootID, sessionKey, (prevList) => {
      const list = [...(prevList || [])];
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (
        last &&
        (last.role === "agent" || last.role === "assistant") &&
        hasSameMindFSID(last, mindfsID)
      ) {
        // **普通拼接**。片段的唯一来源是「服务端事件流」，而事件只会投递一次：
        // 挂载会话时走的是**快照重建**（服务端回 `reset:true` 的整批，客户端先清空
        // 瞬时尾巴再照单应用，见 session.ts 的 dropTransientExchanges），
        // 实时事件则是新事件。两边都不会把同一片再送一遍 ——
        // 曾经为此存在的 mergeStreamedText（子串包含判据）已随之删除。
        //
        // CUSTOM(G-BI)：拼接前必须比对 mindfs_id。同正文不同 id 是**两个回合**，
        // 拼在一起会把上一轮的尾巴并进这一轮（实测：相邻两轮各渲染一遍）。
        list[list.length - 1] = {
          ...last,
          ...(mindfsID ? { mindfs_id: mindfsID } : {}),
          agent: runtimeMeta.agent || last.agent,
          model: runtimeMeta.model || last.model,
          model_display_name:
            runtimeMeta.model_display_name || last.model_display_name,
            mode: runtimeMeta.mode || last.mode,
            effort: runtimeMeta.effort || last.effort,
            fast_service: runtimeMeta.fast_service || last.fast_service,
            content: String(last.content || "") + String(content || ""),
          timestamp: now,
        };
        return list;
      }
      list.push({
        role: "agent",
        ...(mindfsID ? { mindfs_id: mindfsID } : {}),
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

  return { upsertSessionCache, resolveRuntimeMetaForSession, appendAgentChunkForSession };
}
