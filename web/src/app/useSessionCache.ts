/**
 * 会话缓存：内存里的 Session 快照表 + 加载状态/失败记录/LRU 淘汰。
 *
 * 为什么单独抽：这块是 App.tsx 里唯一有**明确数据结构 + 明确策略**的状态 ——
 * 按条数封顶（64）+ 按最近写入时间 LRU 淘汰，加载失败要留痕迹（404 永久、其余退避 5s）。
 * 策略曾经直接写在 App.tsx 里，与几十个读写点混在一起，改策略要翻遍全文件。
 *
 * 对外只暴露 refs（不做方法封装）：缓存的读写点遍布 App.tsx 与 useSessionStreamCache，
 * 都是命令式的 `ref.current[key]` 访问，包一层方法反而要把 60 处调用点全改一遍。
 * 保持 ref 语义与**原始变量名/声明形状**，调用点零改动，源码正则断言也不用动。
 */

import { useCallback, useRef } from "react";
import type { Session, SyncSessionResult } from "../services/session";

/** 内存会话缓存上限。IDB 那份是 500 条 / 200KB，内存这份刻意压到 64 ——
 *  长时间浏览会堆到几百 MB，标签页逐渐卡死然后崩。 */
export const SESSION_CACHE_MAX_ENTRIES = 64;

export type SessionCache = {
  /** 会话快照表 */
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  /** 是否**成功**加载过（失败不置位，否则 404 每轮都重走全量拉取） */
  loadedSessionRef: React.RefObject<Record<string, boolean>>;
  /** 进行中的加载 promise（去重：同一 key 不并发拉两次） */
  loadingSessionRef: React.RefObject<Partial<Record<string, Promise<SyncSessionResult>>>>;
  /** 加载失败记录：404 = 永久（等选中项变化或显式标脏才解禁）；其余退避 5s */
  sessionLoadFailureRef: React.RefObject<Record<string, { at: number; status: number }>>;
  /** 最近写入时间，LRU 淘汰用（流式写入会让活跃会话一直新鲜） */
  sessionCacheAccessRef: React.RefObject<Record<string, number>>;
  /** 标脏键集合：会话内容变了但还没重新拉 */
  staleSessionKeysRef: React.RefObject<Set<string>>;
  /** 按条数封顶 + LRU 淘汰。写入后调用。 */
  enforceSessionCacheBound: () => void;
};

export function useSessionCache(): SessionCache {
  const sessionCacheRef = useRef<Record<string, Session>>({});
  const loadedSessionRef = useRef<Record<string, boolean>>({});
  const loadingSessionRef = useRef<Partial<Record<string, Promise<SyncSessionResult>>>>({});
  // CUSTOM(G-AU): 拉取失败必须留下痕迹，否则「加载失败」在状态里和「还没加载」长得
  // 一模一样 —— 见 loadSession effect 的守卫：`loadedSessionRef` 只在**成功**时置位，
  // 于是 404 的会话每一轮都重新走一遍全量拉取，没有任何终点。
  // 404 = 永久（等选中项变化或显式标脏才解禁）；其余错误退避 5s。
  const sessionLoadFailureRef = useRef<Record<string, { at: number; status: number }>>({});
  // CUSTOM(G-AU): 内存会话缓存**没有上限**，而 IDB 那份有（500 条 / 200KB，
  // toPersistentSession）。长时间浏览会堆到几百 MB → 标签页逐渐卡死然后崩。
  // 按条数封顶 + 按最近写入时间 LRU 淘汰（流式写入会让活跃会话一直新鲜）。
  const sessionCacheAccessRef = useRef<Record<string, number>>({});
  const staleSessionKeysRef = useRef<Set<string>>(new Set());
  const enforceSessionCacheBound = useCallback(() => {
    const cache = sessionCacheRef.current;
    const keys = Object.keys(cache);
    if (keys.length <= SESSION_CACHE_MAX_ENTRIES) return;
    const access = sessionCacheAccessRef.current;
    const evictCount = keys.length - SESSION_CACHE_MAX_ENTRIES;
    keys
      .map((key) => ({ key, at: access[key] || 0 }))
      .sort((a, b) => a.at - b.at)
      .slice(0, evictCount)
      .forEach(({ key }) => {
        delete cache[key];
        delete access[key];
      });
  }, []);

  return {
    sessionCacheRef,
    loadedSessionRef,
    loadingSessionRef,
    sessionLoadFailureRef,
    sessionCacheAccessRef,
    staleSessionKeysRef,
    enforceSessionCacheBound,
  };
}
