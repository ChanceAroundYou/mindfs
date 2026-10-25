import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import type { RealtimeWsHandlersLocal } from "./realtimeWsHandlers";
import { normalizeFastService } from "./appTask";
import { normalizeMode, normalizeSessionAuxFlags } from "./appSession";
import type { SessionItem } from "./appSession";
import type { RelatedWorktree, Session } from "../services/session";

// session.meta.updated / session.related_files.updated 两个 handler，
// 从 realtimeWsHandlers.ts 抽出；与主工厂共用同一 (ctx, local) 契约。

export function createSessionMetaHandlers(
  ctx: RealtimeEventsContext,
  local: RealtimeWsHandlersLocal,
): Record<string, (event: any, payload: any) => void> {
  const {
    refs: { boundSessionByRootRef, selectedSessionRef, sessionCacheRef, sessionsRef },
    setters: { setDrawerSessionForRoot, setSelectedSession, setSessions },
    actions: {
      bumpCacheVersion,
      loadSessionsForRoot,
      playNotificationSound,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      scheduleMultiProjectSessionReload,
      updateSessionRelatedWorktreeForKey,
    },
    values: { multiProjectSessionsEnabled, rootSessionKey, scopedRootKey },
  } = ctx;
  const { refreshSessionRelatedFiles } = local;

  return {
    "session.meta.updated": (event: any, payload: any) => {
        if (
          typeof payload?.root_id === "string" &&
          typeof payload?.session?.key === "string"
        ) {
          const rootID = payload.root_id;
          const sessionKey = payload.session.key;
          const cacheKey = rootSessionKey(rootID, sessionKey);
          const cached =
            sessionCacheRef.current[cacheKey] ||
            ({
              key: sessionKey,
              root_id: rootID,
              type: normalizeMode(payload.session.type),
              name:
                typeof payload.session.name === "string"
                  ? payload.session.name
                  : "",
              created_at: payload.session.updated_at || new Date().toISOString(),
              updated_at: payload.session.updated_at || new Date().toISOString(),
              exchanges: [],
            } as Session);
          sessionCacheRef.current[cacheKey] = {
              ...cached,
              name:
                typeof payload.session.name === "string"
                  ? payload.session.name
                  : cached.name,
              agent:
                typeof payload.session.agent === "string"
                  ? payload.session.agent
                  : (cached as any).agent,
              model:
                typeof payload.session.model === "string"
                  ? payload.session.model
                  : (cached as any).model,
              mode:
                typeof payload.session.mode === "string"
                  ? payload.session.mode
                  : (cached as any).mode,
              effort:
                typeof payload.session.effort === "string"
                  ? payload.session.effort
                  : (cached as any).effort,
              fast_service:
                normalizeFastService(payload.session.fast_service) ||
                normalizeFastService((cached as any).fast_service),
              plan_mode:
                typeof payload.session.plan_mode === "boolean"
                  ? payload.session.plan_mode
                  : !!(cached as any).plan_mode,
              parent_session_key:
                typeof payload.session.parent_session_key === "string"
                  ? payload.session.parent_session_key
                  : (cached as any).parent_session_key,
              parent_tool_call_id:
                typeof payload.session.parent_tool_call_id === "string"
                  ? payload.session.parent_tool_call_id
                  : (cached as any).parent_tool_call_id,
              source:
                typeof payload.session.source === "string"
                  ? payload.session.source
                  : (cached as any).source,
              task_id:
                typeof payload.session.task_id === "string"
                  ? payload.session.task_id
                  : (cached as any).task_id,
              related_worktree:
                payload.session.related_worktree !== undefined
                  ? payload.session.related_worktree
                  : (cached as any).related_worktree,
              // work_dir 与 related_worktree 同一合并语义：payload 带才覆盖，不带沿用缓存。
              work_dir:
                typeof payload.session.work_dir === "string"
                  ? payload.session.work_dir
                  : (cached as any).work_dir,
              // worktree_missing 与 aux_flags 都是服务端派生：payload 带才覆盖，
              // 不带（旧广播/部分更新）沿用缓存 —— 与 related_worktree 同一合并语义。
              worktree_missing:
                typeof payload.session.worktree_missing === "boolean"
                  ? payload.session.worktree_missing
                  : (cached as any).worktree_missing,
              worktree_active:
                typeof payload.session.worktree_active === "boolean"
                  ? payload.session.worktree_active
                  : (cached as any).worktree_active,
              aux_flags:
                payload.session.aux_flags !== undefined
                  ? normalizeSessionAuxFlags(payload.session.aux_flags)
                  : (cached as any).aux_flags,
              pinned_at:
                payload.session.pinned_at !== undefined
                  ? payload.session.pinned_at || undefined
                  : (cached as any).pinned_at,
              updated_at: payload.session.updated_at || cached.updated_at,
            } as Session;
          bumpCacheVersion();
          if (
            (selectedSessionRef.current?.key ||
              selectedSessionRef.current?.session_key) === sessionKey
          ) {
            setSelectedSession((prev) =>
              prev
                ? ({
                    ...(prev as any),
                    name:
                      typeof payload.session.name === "string"
                        ? payload.session.name
                        : prev.name,
                    agent:
                      typeof payload.session.agent === "string"
                        ? payload.session.agent
                        : (prev as any).agent,
                    model:
                      typeof payload.session.model === "string"
                        ? payload.session.model
                        : (prev as any).model,
                    mode:
                      typeof payload.session.mode === "string"
                        ? payload.session.mode
                        : (prev as any).mode,
                    effort:
                      typeof payload.session.effort === "string"
                        ? payload.session.effort
                        : (prev as any).effort,
                    fast_service:
                      normalizeFastService(payload.session.fast_service) ||
                      normalizeFastService((prev as any).fast_service),
                    plan_mode:
                      typeof payload.session.plan_mode === "boolean"
                        ? payload.session.plan_mode
                        : !!(prev as any).plan_mode,
                    parent_session_key:
                      typeof payload.session.parent_session_key === "string"
                        ? payload.session.parent_session_key
                        : (prev as any).parent_session_key,
                    parent_tool_call_id:
                      typeof payload.session.parent_tool_call_id === "string"
                        ? payload.session.parent_tool_call_id
                        : (prev as any).parent_tool_call_id,
                    source:
                      typeof payload.session.source === "string"
                        ? payload.session.source
                        : (prev as any).source,
                    task_id:
                      typeof payload.session.task_id === "string"
                        ? payload.session.task_id
                        : (prev as any).task_id,
                    related_worktree:
                      payload.session.related_worktree !== undefined
                        ? payload.session.related_worktree
                        : (prev as any).related_worktree,
                    work_dir:
                      typeof payload.session.work_dir === "string"
                        ? payload.session.work_dir
                        : (prev as any).work_dir,
                    worktree_missing:
                      typeof payload.session.worktree_missing === "boolean"
                        ? payload.session.worktree_missing
                        : (prev as any).worktree_missing,
                    worktree_active:
                      typeof payload.session.worktree_active === "boolean"
                        ? payload.session.worktree_active
                        : (prev as any).worktree_active,
                    aux_flags:
                      payload.session.aux_flags !== undefined
                        ? normalizeSessionAuxFlags(payload.session.aux_flags)
                        : (prev as any).aux_flags,
                    pinned_at:
                      payload.session.pinned_at !== undefined
                        ? payload.session.pinned_at || undefined
                        : (prev as any).pinned_at,
                    updated_at: payload.session.updated_at || prev.updated_at,
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
          // 列表项就地更新 aux 标志：aux 变化**不改会话的 updated_at**，所以下面
          // 按 updated_at 增量拉的列表请求根本带不回这条会话 —— 列表项会一直拿着
          // 旧值，徽标要等下一次全量刷新才灭（症状「答完不灭，要会话结束才更新」）。
          if (payload.session.aux_flags !== undefined) {
            const auxFlags = normalizeSessionAuxFlags(payload.session.aux_flags);
            // 「轮到你了」只在 false→true 的那一下响：同一个提问会随 meta 广播重复下发，
            // 只在跳变时放音，否则答完再刷新会一直响。
            // 注意读 `cached`（覆盖前的快照）：上面已经把新值写回 sessionCacheRef 了。
            const wasWaiting = !!normalizeSessionAuxFlags(
              (cached as any)?.aux_flags,
            )?.ask_user_waiting;
            if (!wasWaiting && auxFlags?.ask_user_waiting) {
              playNotificationSound("intervene");
            }
            setSessions((prev) =>
              prev.map((item) => {
                const itemKey = String(item.key || item.session_key || "");
                if (itemKey !== sessionKey) return item;
                return { ...item, aux_flags: auxFlags } as SessionItem;
              }),
            );
          }
          const relatedWorktreePath =            typeof payload.session.related_worktree?.path === "string"
              ? payload.session.related_worktree.path.trim()
              : "";
          if (relatedWorktreePath) {
            void refreshTaskWorktree(rootID, relatedWorktreePath, false);
          }
          // afterTime 增量按 updated_at 严格大于排除刚创建/刚更新的会话（乐观项 timestamp 与
          // 服务端 updated_at 的竞态，session.done 处有同款注释）：新 key 首次经 WS 露面时
          // 必须 replace 全量重拉，否则要等手动同步才出现在列表里。
          const listHasKey = sessionsRef.current.some(
            (item) =>
              String(item.key || item.session_key || "") === sessionKey,
          );
          if (listHasKey) {
            const newest = sessionsRef.current[0]?.updated_at || "";
            void loadSessionsForRoot(
              rootID,
              newest ? { afterTime: newest } : { replace: true },
            );
          } else {
            void loadSessionsForRoot(rootID, { replace: true });
          }
          if (multiProjectSessionsEnabled) {
            scheduleMultiProjectSessionReload();
          }
        }
    },
    "session.related_files.updated": (event: any, payload: any) => {
        const rootID =
          typeof payload?.root_id === "string" ? payload.root_id : "";
        const sessionKey =
          typeof payload?.session_key === "string" ? payload.session_key : "";
        if (rootID && sessionKey) {
          void refreshSessionRelatedFiles(rootID, sessionKey);
          refreshTasksForRelatedSession(rootID, sessionKey);
          const cachedSession =
            sessionCacheRef.current[rootSessionKey(rootID, sessionKey)];
          const parentSessionKey = String(
            cachedSession?.parent_session_key || "",
          ).trim();
          if (parentSessionKey) {
            void refreshSessionRelatedFiles(rootID, parentSessionKey);
            refreshTasksForRelatedSession(rootID, parentSessionKey);
          }
          if (payload?.related_worktree && typeof payload.related_worktree === "object") {
            updateSessionRelatedWorktreeForKey(
              rootID,
              sessionKey,
              payload.related_worktree as RelatedWorktree,
            );
          }
        }
        return;
    },

  };
}
