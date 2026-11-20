import { useCallback, useRef, useState } from "react";
import { scopeKey } from "../shared/scope";
import type { ManagedRootPayload } from "./appMisc";

/** 当前项目/节点作用域与跨节点 payload 索引。 */
export function useAppRootScopeState() {
  const [currentRootId, setCurrentRootId] = useState<string | null>(null);
  const currentRootIdRef = useRef<string | null>(null);
  const [currentRootNodeId, setCurrentRootNodeId] = useState<string | null>(null);
  const currentRootNodeIdRef = useRef<string | null>(null);
  const managedRootByIdRef = useRef<Record<string, ManagedRootPayload>>({});
  // 多节点同名项目的完整索引：nodeId::rootId → payload（byId 回退表只保留一份）
  const managedRootByKeyRef = useRef<Record<string, ManagedRootPayload>>({});

  /**
   * 裸 rootId 该发往哪台机器。
   *
   * 规则（与 services/net/rootNode 同源）：当前选中的项目 → 它的选中节点；
   * 否则只有**唯一归属**才解析得出 —— `managedRootById` 现在已经不装同名项目了
   * （`indexManagedRoots` 只在唯一归属时填那一格），查不到就是「必须显式带节点」。
   */
  const getNodeIdForRoot = useCallback((rootId: string): string | undefined => {
    const rid = String(rootId || "").trim();
    if (!rid) return undefined;
    // 当前选中的项目优先按其选中节点路由，避免同名项目被多节点表覆盖到错误节点
    if (rid === String(currentRootIdRef.current || "")) {
      const nid = String(currentRootNodeIdRef.current || "").trim();
      if (nid) return nid;
    }
    const entry = (managedRootByIdRef.current as Record<string, any>)[rid];
    const nid = String(entry?._nodeId || "").trim();
    return nid || undefined;
  }, []);

  /** 带显式节点的版本：跨节点列表里的条目必须走这条，别让裸 rootId 决定命运。 */
  const getNodeIdForProject = useCallback(
    (rootId: string, nodeId?: string | null): string | undefined => {
      const explicit = String(nodeId || "").trim();
      if (explicit) return explicit;
      return getNodeIdForRoot(rootId);
    },
    [getNodeIdForRoot],
  );

  const scopedRootKey = useCallback(
    (rootId: string): string =>
      scopeKey(getNodeIdForRoot(String(rootId || "")) ?? "", String(rootId || "")),
    [getNodeIdForRoot],
  );

  return {
    currentRootId, setCurrentRootId, currentRootIdRef, currentRootNodeId, setCurrentRootNodeId,
    currentRootNodeIdRef, managedRootByIdRef, managedRootByKeyRef, getNodeIdForRoot,
    getNodeIdForProject, scopedRootKey,
  };
}
