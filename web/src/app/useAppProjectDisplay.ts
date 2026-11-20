import { useCallback, useMemo } from "react";

import { scopeKey } from "../shared/scope";
import type { ManagedRootPayload } from "./appMisc";
import { taskProjectScopeKey, type TaskInlineEditState } from "./appTask";

export type UseAppProjectDisplayParams = {
  currentRootId: string | null;
  currentRootNodeId: string | null;
  currentRootNodeIdRef: React.RefObject<string | null>;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: React.RefObject<Set<string>>;
  rootEntries: import("../services/prefs/directorySort").FileEntry[];
  getNodeIdForRoot: (rootId: string) => string | undefined;
  taskInlineEdit: TaskInlineEditState | null;
};

/**
 * 项目「叫什么、什么颜色」，以及新建任务面板的项目下拉选项。
 *
 * 三者是一根链：项目名和节点色都从 registry 的两张 ref 表里查，下拉选项再把
 * 这两者拼成 label/color。所以放一起 —— 单独搬走任何一个，调用方都得同时
 * 拿到另外两个才用得上。
 *
 * 查表一律走 ref 而不是 state：这些函数被大量 useCallback 当依赖用，
 * 依赖 state 会让它们每轮都换引用，下游全部跟着重建。
 */
export function useAppProjectDisplay(params: UseAppProjectDisplayParams) {
  const {
    currentRootId,
    currentRootNodeId,
    currentRootNodeIdRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIdsRef,
    rootEntries,
    getNodeIdForRoot,
    taskInlineEdit,
  } = params;

  /**
   * 项目「叫什么」。
   *
   * `nodeId` 给了就按**那台机器**的那份查（`byKey`）—— 同名项目在多台机器上可能
   * 有不同的显示名（pc 的 `docs` 叫「AIS文档」，fn 的叫 `docs`），工作台分组必须
   * 显示分组自己那台的名字，否则 fn 的组会顶着 pc 的名字。
   */
  const getRootDisplayName = useCallback((rootId: string | null | undefined, nodeId?: string | null): string => {
    const id = String(rootId || "").trim();
    if (!id) return "";
    const nid = String(nodeId || "").trim();
    const entry: any = nid
      ? managedRootByKeyRef.current[scopeKey(nid, id)]
      : managedRootByIdRef.current[id];
    const dn = String(entry?.display_name || "").trim();
    if (dn) return dn;
    return String(entry?.id || id);
  }, []);

  const getDisplayNodeColor = useCallback((rootId: string, nodeId?: string | null): string | null => {
    const rid = String(rootId || "").trim();
    if (!rid) return null;
    const explicit = String(nodeId || "").trim();
    if (explicit) {
      const c = String((managedRootByKeyRef.current as any)[scopeKey(explicit, rid)]?._nodeColor || "").trim();
      if (c) return c;
    }
    const curNid = String(currentRootNodeIdRef.current || "").trim();
    if (curNid) {
      const scoped = scopeKey(curNid, rid);
      const c = String((managedRootByKeyRef.current as any)[scoped]?._nodeColor || "").trim();
      if (c) return c;
    }
    return String((managedRootByIdRef.current as any)[rid]?._nodeColor || "").trim() || null;
  }, []);

  /**
   * 工作台/深链上给「同名项目」加节点后缀：`AIS文档 · fn`。
   *
   * 只在这一台之外还有别的机器拥有同一个 id 时才加 —— 唯一归属的项目不需要噪声。
   * 与新建任务下拉里 `label · nodeName` 是同一套口径。
   */
  const getProjectLabel = useCallback((rootId: string, nodeId?: string | null): string => {
    const id = String(rootId || "").trim();
    if (!id) return "";
    const nid = String(nodeId || "").trim();
    const base = getRootDisplayName(id, nid);
    if (!nid) return base;
    const owners = new Set<string>();
    for (const key of Object.keys(managedRootByKeyRef.current || {})) {
      const idx = key.indexOf("::");
      if (idx < 0) continue;
      if (key.slice(idx + 2) !== id) continue;
      const owner = key.slice(0, idx).trim();
      if (owner) owners.add(owner);
    }
    if (owners.size <= 1) return base;
    const nodeName = String((managedRootByKeyRef.current as any)[scopeKey(nid, id)]?._nodeName || nid).trim();
    return nodeName ? `${base} · ${nodeName}` : base;
  }, [getRootDisplayName]);

  // 新建任务面板上的「项目」下拉选项。只有工作台发起的面板会用到它（看板入口
  // 项目已定），范围取跨项目会话那份清单，挂在 taskInlineEdit.targetRootId 上
  // 是为了项目增删后重算一次，不额外订阅一份 state。
  //
  // 项目名各用各的节点色，和工作台项目行、DefaultListView 的项目徽章是同一套口径。
  // 位置必须在 getDisplayNodeColor 之后 —— const 不提升，提前引用会吃到 TDZ。
  const taskCreateProjectOptions = useMemo(() => {

    const entries = rootEntries.filter((entry) => String(entry.path || "").trim());

    if (entries.length === 0 && currentRootId) {

      const nodeId = currentRootNodeId || getNodeIdForRoot(currentRootId) || "";

      return [{ value: taskProjectScopeKey(currentRootId, nodeId), label: getRootDisplayName(currentRootId) || currentRootId, color: getDisplayNodeColor(currentRootId) || undefined }];

    }

    const rootCounts = new Map<string, number>();

    for (const entry of entries) {

      const rootId = String(entry.path || "").trim();

      rootCounts.set(rootId, (rootCounts.get(rootId) || 0) + 1);

    }

    return entries.map((entry) => {

      const rootId = String(entry.path || "").trim();

      const nodeId = String((entry as any)._nodeId || "").trim();

      const nodeName = String((entry as any)._nodeName || nodeId).trim();

      const label = String(entry.name || getRootDisplayName(rootId) || rootId);

      return {

        value: taskProjectScopeKey(rootId, nodeId),

        label: (rootCounts.get(rootId) || 0) > 1 && nodeName ? `${label} · ${nodeName}` : label,

        color: String((entry as any)._nodeColor || "").trim() || undefined,

      };

    });

  }, [currentRootId, currentRootNodeId, getNodeIdForRoot, getRootDisplayName, getDisplayNodeColor, rootEntries, taskInlineEdit?.targetRootId]);


  return { getRootDisplayName, getDisplayNodeColor, getProjectLabel, taskCreateProjectOptions };
}