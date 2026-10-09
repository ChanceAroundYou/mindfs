/**
 * root.changed WS 事件处理器。
 *
 * 从 realtimeWsHandlers.ts 拆出（2026-10-10）：该 handler 56 行，处理根目录
 * 变更事件（重命名、显示名变更、通用刷新）。
 *
 * CUSTOM(G-BA)：上游没有这个拆分。合上游时若 realtimeWsHandlers.ts 被整体覆盖，
 * 需要把本文件的内容合回去（或重新拆分），否则 root.changed 处理会丢失。
 */
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import type { ManagedRootPayload } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { mapManagedRootsToEntries } from "./appMisc";
import { setRootNodeMap } from "../services/net/rootNode";

export function handleRootChanged(
  ctx: RealtimeEventsContext,
  event: any,
  payload: any,
): void {
  const {
    refs: {
      currentRootIdRef,
      actionHandlersRef,
      managedRootByIdRef,
      managedRootByKeyRef,
      managedRootIdsRef,
    },
    setters: {
      setRootEntries,
      setMultiProjectSessionGroups,
    },
    actions: {
      applyManagedRootRename,
      refreshManagedRoots,
    },
  } = ctx;

  if (
    payload?.action === "renamed" &&
    typeof payload?.old_root_id === "string" &&
    typeof payload?.root_id === "string"
  ) {
    const rootPayload =
      payload?.root && typeof payload.root === "object"
        ? ({ ...(payload.root as ManagedRootPayload), id: payload.root_id } as ManagedRootPayload)
        : null;
    if (!rootPayload) {
      return;
    }
    applyManagedRootRename(payload.old_root_id, rootPayload);
    if (currentRootIdRef.current === payload.old_root_id) {
      void actionHandlersRef.current.open_dir({
        path: payload.root_id,
        root: payload.root_id,
        isRoot: true,
        forceDirectory: true,
        preservePluginQuery: true,
      });
    }
    return;
  }
  if (
    payload?.action === "display_name_changed" &&
    typeof payload?.root_id === "string"
  ) {
    const rootId = String(payload.root_id).trim();
    const rootPayload =
      payload?.root && typeof payload.root === "object"
        ? ({ ...(payload.root as ManagedRootPayload), id: rootId } as ManagedRootPayload)
        : null;
    if (!rootPayload || !rootId) {
      return;
    }
    const merged = { ...(managedRootByIdRef.current[rootId] || {} as ManagedRootPayload), ...rootPayload, id: rootId } as ManagedRootPayload;
    const nextById = { ...managedRootByIdRef.current, [rootId]: merged };
    managedRootByIdRef.current = nextById as Record<string, ManagedRootPayload>;
    const evtNid = String((merged as any)._nodeId || "").trim();
    if (evtNid) managedRootByKeyRef.current[rootNodeKey(evtNid, rootId)] = merged;
    setRootNodeMap(nextById as Record<string, any>);
    const ids = Array.from(managedRootIdsRef.current);
    setRootEntries(mapManagedRootsToEntries(ids.map((id) => nextById[id]).filter(Boolean) as ManagedRootPayload[]));
    setMultiProjectSessionGroups((prev) =>
      prev.map((g) =>
        g.rootId === rootId
          ? { ...g, rootName: String((merged as any)?.display_name || (merged as any)?.id || g.rootName || rootId) }
          : g,
      ),
    );
    return;
  }
  void refreshManagedRoots();
}
