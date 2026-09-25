/**
 * 跨项目工作台的数据层：跨节点扇出拉任务总览，再按项目聚合成组。
 *
 * 为什么是前端扇出：后端 Overview() 遍历的是**本节点**的 Roots.ListRoots()，
 * 它根本不知道自己被哪个节点调用 —— 请求打到哪台机器，返回的就是那台的项目。
 * 所以「把 nodeId 塞进 TaskOverviewItem」后端做不了，也不必做：前端对每个节点各发一次，
 * 自己打标。扇出的形状照搬 loadMultiProjectSessionGroups（App.tsx 内）：
 * 同一套节点清单、同一套去重键、同一套初始化竞态回退、同样「一节点失败不阻塞其余」。
 *
 * 聚合以 managedRootIds 为**基准**而不是以返回的 items 为基准：后端只 append 有任务的项目
 * （Overview 里逐 root 拉、拉到才 append），拿 items 建组会让「一个任务都没有的项目」
 * 从工作台上彻底消失 —— 而空项目恰恰是最需要被看到的那批。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getNodes } from "../services/nodeRegistry";
import { scopeKey } from "../services/scope";
import { fetchTasksOverview, type KanbanTask, type TaskOverviewItem } from "../services/tasks";
import { isTerminalKanbanTask } from "./appTask";

/** 跨项目任务条 = 后端 TaskOverviewItem + 前端扇出时打的节点标（后端不返这个字段） */
export type WorkspaceTaskItem = TaskOverviewItem & { nodeId: string };

/** 一次扇出的原始结果，尚未按项目建组 */
export type WorkspaceTaskPool = {
  items: WorkspaceTaskItem[];
  loading: boolean;
  /** 本次扇出覆盖到的节点 */
  nodeIds: string[];
  refresh: () => void;
};

/** 工作台按项目聚合出的一组（一个项目一条） */
export type WorkspaceProjectGroup = {
  /** 复合键 scopeKey(nodeId, rootId) —— 同名项目跨节点不冲突 */
  key: string;
  rootId: string;
  rootName: string;
  nodeId: string;
  /** 节点色；取不到时为 null（渲染层回退 var(--text-secondary)） */
  color: string | null;
  /** 非终态任务，按 updated_at 倒序 */
  active: WorkspaceTaskItem[];
  /** waiting_user / pending —— 顶部「需要你」捞的就是这批 */
  blocked: WorkspaceTaskItem[];
  /** 终态：success / fail / cancelled */
  ended: WorkspaceTaskItem[];
  /** 有活跃会话（由多项目会话组派生，不新增请求） */
  sessionCount: number;
  /** 该项目既无任务也无会话 */
  isEmpty: boolean;
};

export type WorkspaceBoard = {
  projects: WorkspaceProjectGroup[];
  /** 全部项目的 blocked 汇总，顶部条带渲染用；按 updated_at 倒序 */
  blockedAll: WorkspaceTaskItem[];
  loading: boolean;
  refresh: () => void;
};

export type WorkspaceBoardFilter = "all" | "active" | "blocked";

const EMPTY_ITEMS: WorkspaceTaskItem[] = [];

const byUpdatedDesc = (a: WorkspaceTaskItem, b: WorkspaceTaskItem): number =>
  String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || ""));

/** 跨节点/跨机器重名项目都可能出现，同一 rootId 可能有多个节点副本 */
function toRootEntries(rootIds: string[], getNodeId: (rootId: string) => string | undefined): Array<{ rootId: string; nodeId: string }> {
  const seen = new Set<string>();
  const entries: Array<{ rootId: string; nodeId: string }> = [];
  const push = (rootId: string, nodeId: string) => {
    const key = scopeKey(nodeId, rootId);
    if (seen.has(key)) return;
    seen.add(key);
    entries.push({ rootId, nodeId });
  };
  for (const rootId of rootIds) {
    if (!rootId) continue;
    // 当前选中的项目优先按其选中节点路由，避免同名项目被多节点表覆盖到错误节点
    const nodeId = String(getNodeId(rootId) || "").trim();
    if (nodeId) push(rootId, nodeId);
  }
  return entries;
}

export function useWorkspaceBoard(params: {
  /** 只有切到 workspace 才拉数 */
  enabled: boolean;
  /** 整包重拉的版本号：WS 每次 task.updated 自增（首屏 + 兜底），避免高频事件把扇出打爆 */
  refreshToken: number;
  /** 聚合基准：没有任务的项目也必须在工作台上出现 */
  managedRootIds: string[];
  getRootDisplayName: (rootId: string | null | undefined) => string;
  getNodeColor: (rootId: string) => string | null;
  getNodeId: (rootId: string) => string | undefined;
  /** getNodes() 未就绪时的回退节点，与会话侧 loadMultiProjectSessionGroups 同一口径 */
  fallbackNodeId: string;
  /** 已有的跨项目会话组，只用来算 sessionCount，不新增请求 */
  sessionCounts: Map<string, number>;
  filter: WorkspaceBoardFilter;
}): WorkspaceBoard {
  const {
    enabled, refreshToken, managedRootIds, getRootDisplayName,
    getNodeColor, getNodeId, fallbackNodeId, sessionCounts, filter,
  } = params;
  const [items, setItems] = useState<WorkspaceTaskItem[]>(EMPTY_ITEMS);
  const [loading, setLoading] = useState(false);
  const [localToken, setLocalToken] = useState(0);
  // 这些每渲染都会新造一份，塞进依赖会把 effect 抖成死循环 —— 用 ref 读最新值
  const cfgRef = useRef(params);
  cfgRef.current = params;

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setLoading(true);
    void (async () => {
      const cfg = cfgRef.current;
      // 1) 节点全集 = getNodes() ∪ managedRootIds 解析出的节点。
      //    两个来源都要：getNodes() 在初始化竞态下可能还是空，反过来本地当前节点
      //    也可能还没进 getNodes()，取并集才不漏（与 App.tsx 会话侧同一口径）。
      const fromNodes = getNodes()
        .map((n) => String((n as any)?.id || "").trim())
        .filter(Boolean);
      const fromRoots = cfg.managedRootIds
        .map((rid) => String(cfg.getNodeId(rid) || "").trim())
        .filter(Boolean);
      const nodeIds = Array.from(new Set([...fromNodes, ...fromRoots]));
      const targets = nodeIds.length > 0
        ? nodeIds
        : [String(cfg.fallbackNodeId || "").trim()].filter(Boolean);
      if (targets.length === 0) {
        if (!cancelled) {
          setItems(EMPTY_ITEMS);
          setLoading(false);
        }
        return;
      }
      // 2) 扇出：一个节点失败返回空，不阻塞其余节点
      const results = await Promise.all(
        targets.map(async (nid) => {
          try {
            const list = await fetchTasksOverview(nid || undefined);
            return list.map((item) => ({ ...item, nodeId: nid })) as WorkspaceTaskItem[];
          } catch {
            return [] as WorkspaceTaskItem[];
          }
        }),
      );
      if (cancelled) return;
      // 3) 去重：键里带 nodeId，同名项目在两台机器上各算一条
      const byKey = new Map<string, WorkspaceTaskItem>();
      for (const item of results.flat()) {
        if (!item?.task?.id || !item.root_id) continue;
        const key = `${item.nodeId}::${item.root_id}::${item.task.id}`;
        const prev = byKey.get(key);
        if (!prev || String(item.task.updated_at || "") > String(prev.task.updated_at || "")) byKey.set(key, item);
      }
      setItems(Array.from(byKey.values()));
      setLoading(false);
    })();
    return () => { cancelled = true; };
  }, [enabled, refreshToken, localToken]);

  const refresh = useCallback(() => setLocalToken((n) => n + 1), []);

  const projects = useMemo<WorkspaceProjectGroup[]>(() => {
    const byProject = new Map<string, WorkspaceTaskItem[]>();
    for (const item of items) {
      const key = scopeKey(item.nodeId, item.root_id);
      const bucket = byProject.get(key);
      if (bucket) bucket.push(item);
      else byProject.set(key, [item]);
    }
    return toRootEntries(managedRootIds, getNodeId)
      .map(({ rootId, nodeId }) => {
        const key = scopeKey(nodeId, rootId);
        const bucket = (byProject.get(key) || []).slice().sort(byUpdatedDesc);
        const active = bucket.filter((i) => !isTerminalKanbanTask(i.task));
        const blocked = active.filter((i) => isBlockedTask(i.task));
        const ended = bucket.filter((i) => isTerminalKanbanTask(i.task));
        const sessionCount = sessionCounts.get(key) || 0;
        return {
          key,
          rootId,
          rootName: getRootDisplayName(rootId) || rootId,
          nodeId,
          color: getNodeColor(rootId) || null,
          active,
          blocked,
          ended,
          sessionCount,
          isEmpty: bucket.length === 0 && sessionCount === 0,
        };
      })
      .filter((group) => {
        if (filter === "active") return group.active.length > 0 || group.sessionCount > 0;
        if (filter === "blocked") return group.blocked.length > 0;
        return true; // 「全部」下空项目也要出现 —— 它才需要被看到
      });
  }, [items, managedRootIds, getNodeId, getRootDisplayName, getNodeColor, sessionCounts, filter]);

  const blockedAll = useMemo(
    () => projects.flatMap((group) => group.blocked).sort(byUpdatedDesc),
    [projects],
  );

  return { projects, blockedAll, loading, refresh };
}

export function isBlockedTask(task: KanbanTask): boolean {
  return task.status === "waiting_user" || task.status === "pending";
}
