/**
 * 跨项目工作台的数据层：跨节点扇出拉任务总览，再按项目聚合成组。
 *
 * 为什么是前端扇出：后端 Overview() 遍历的是**本节点**的 Roots.ListRoots()，
 * 它根本不知道自己被哪个节点调用 —— 请求打到哪台机器，返回的就是那台的项目。
 * 所以「把 nodeId 塞进 TaskOverviewItem」后端做不了，也不必做：前端对每个节点各发一次，
 * 自己打标。扇出的形状照搬 loadMultiProjectSessionGroups（App.tsx 内）：
 * 同一套节点清单、同一套去重键、同一套初始化竞态回退、同样「一节点失败不阻塞其余」。
 *
 * 聚合以 managedRootIds **∪ 任务里出现的 nodeId::root_id** 为基准：
 * 纯 managedRootIds 的话非本机项目即使有任务也不成组（pc 断联恢复后「pc 的任务全没了」，
 * 实测就是这里）；纯 items 的话「一个任务都没有的项目」和「本节点没有、但别的节点有同名项目」
 * 混为一谈 —— 复合键只有对 managedRootIds 逐个求值才认得出来。两者取并集后，
 * 组建出来再按筛选收窄，匹配不到任何任务的组不渲染。
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getNodes } from "../services/nodeRegistry";
import { scopeKey } from "../services/scope";
import { fetchTasksOverview, type KanbanTask, type TaskOverviewItem } from "../services/tasks";

/** 跨项目任务条 = 后端 TaskOverviewItem + 前端扇出时打的节点标（后端不返这个字段） */
export type WorkspaceTaskItem = TaskOverviewItem & { nodeId: string };

/** 工作台按项目聚合出的一组（一个项目一条） */
export type WorkspaceProjectGroup = {
  /** 复合键 scopeKey(nodeId, rootId) —— 同名项目跨节点不冲突 */
  key: string;
  rootId: string;
  rootName: string;
  nodeId: string;
  /** 节点色；取不到时为 null（渲染层回退 var(--text-secondary)） */
  color: string | null;
  /** 当前筛选下这条任务行要显示什么；空则整组不渲染 */
  tasks: WorkspaceTaskItem[];
};

export type WorkspaceBoard = {
  projects: WorkspaceProjectGroup[];
  loading: boolean;
  /** 本轮拉取失败的节点（B3）：面板上要显式说「这些节点没拉到」，别让人以为任务没了 */
  unreachableNodes: Array<{ id: string; name: string }>;
  refresh: () => void;
};

export type WorkspaceBoardFilter = "all" | "active" | "blocked";

const EMPTY_ITEMS: WorkspaceTaskItem[] = [];

const byUpdatedDesc = (a: WorkspaceTaskItem, b: WorkspaceTaskItem): number =>
  String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || ""));

/**
 * 复合键只用 scopeKey(nodeId, rootId) 不够：本地项目的 rootId 与远端同名项目完全一样，
 * scopeKey 也认不出来（后端 Overview 不带 nodeId，前端打标用的就是空串）。所以本地那一路
 * 要用 managedRootIds 里记下的 nodeId 去对齐。
 */
function scopeKeyForItem(item: WorkspaceTaskItem, getNodeId: (rootId: string) => string | undefined): string {
  const rootId = String(item?.root_id || "");
  const nodeId = String(item?.nodeId || "").trim() || String(getNodeId(rootId) || "").trim();
  return scopeKey(nodeId, rootId);
}

/**
 * 逐个 managedRootIds 求值不出来的，交给这个函数兜底。
 *
 * 以前分组来源只有 managedRootIds（本机 registry 的项目 id）当**基准**，于是非本机项目
 * 即便有任务也不成组——「pc 上跑的任务在 pc 断联恢复后消失」就是这么来的。现在改成
 * 两者取并集：managedRootIds 负责本机项目（含一个任务都没有的，配合筛选后不渲染），
 * 任务里出现的 nodeId::root_id 负责补齐本机清单里没有的（通常是别的节点上的项目）。
 */
function toGroupEntries(
  rootIds: string[],
  getNodeId: (rootId: string) => string | undefined,
  byProject: Map<string, WorkspaceTaskItem[]>,
): Array<{ rootId: string; nodeId: string }> {
  const seen = new Set<string>();
  const entries: Array<{ rootId: string; nodeId: string }> = [];
  const push = (rootId: string, nodeId: string) => {
    const key = scopeKey(nodeId, rootId);
    if (!rootId || seen.has(key)) return;
    seen.add(key);
    entries.push({ rootId, nodeId });
  };
  for (const rootId of rootIds) {
    if (!rootId) continue;
    // 当前选中的项目优先按其选中节点路由，避免同名项目被多节点表覆盖到错误节点
    const nodeId = String(getNodeId(rootId) || "").trim();
    if (nodeId) push(rootId, nodeId);
  }
  // 任务里出现、但本机项目清单里没有的组合（比如别的节点上的项目）
  for (const [key, bucket] of byProject) {
    if (seen.has(key) || bucket.length === 0) continue;
    const first = bucket[0];
    push(String(first?.root_id || ""), String(first?.nodeId || "").trim());
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
  filter: WorkspaceBoardFilter;
  /**
   * 取本机已知的该任务最新版本（taskDetailsById 的读取函数）。
   *
   * 为什么工作台要回头读这张表：扇出拉回的那份 items 是**发完就冻结**的快照，
   * 而卡片上所有动作（完成/立即执行/暂停/删除）与 WS 的 task.updated 都只更新
   * taskDetailsById —— 不接这个读取器，点了按钮卡片就纹丝不动。
   * 纯远端任务本机没有这张表，回退扇出原值，行为不变。
   */
  getLiveTask: (taskId: string) => KanbanTask | undefined;
}): WorkspaceBoard {
  const {
    enabled, refreshToken, managedRootIds, getRootDisplayName,
    getNodeColor, getNodeId, fallbackNodeId, filter, getLiveTask,
  } = params;
  const [items, setItems] = useState<WorkspaceTaskItem[]>(EMPTY_ITEMS);
  const [loading, setLoading] = useState(false);
  const [localToken, setLocalToken] = useState(0);
  // 本轮拉取失败的节点（B3）：远端断联时它们的任务会缺席，必须显式提示，
  // 否则「任务凭空消失」和「那边真的没任务」长得一模一样。
  const [unreachableNodes, setUnreachableNodes] = useState<Array<{ id: string; name: string }>>([]);
  // 这些每渲染都会新造一份，塞进依赖会把 effect 抖成死循环 —— 用 ref 读最新值
  const cfgRef = useRef(params);
  cfgRef.current = params;

  // enabled 从 false 翻到 true 的那一次渲染，本地节点可能还没进 getNodes()/managedRootIds。
  // 若 effect 只认依赖值、而那些值此时恰好没变化，切到工作台就会看到一台节点都不扇出。
  const fanoutKey = enabled
    ? `${managedRootIds.join(",")}|${getNodes().map((n) => String((n as any)?.id || "")).join(",")}`
    : "";
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
      // 2) 扇出：一个节点失败返回空，不阻塞其余节点。
      //    失败的节点要**记下来**（B3）：远端挂起时 tasks 拉空，用户看到的是
      //    「pc 上的任务凭空消失了」，而这其实是拉取失败。区分开才能给提示。
      //    挂起由 protectedJSON 的 10s deadline 兜底（见 services/api.ts），
      //    不然 Promise.all 会被一个节点拖住，本机数据也提交不上去。
      const failedNodeIds: string[] = [];
      const results = await Promise.all(
        targets.map(async (nid) => {
          try {
            const list = await fetchTasksOverview(nid || undefined);
            return list.map((item) => ({ ...item, nodeId: nid })) as WorkspaceTaskItem[];
          } catch {
            failedNodeIds.push(nid);
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
      setUnreachableNodes(
        failedNodeIds
          .map((nid) => getNodes().find((n: any) => String(n?.id || "") === nid))
          .filter((n): n is any => !!n)
          .map((n: any) => ({ id: String(n.id), name: String(n.name || n.id) })),
      );
      setLoading(false);
    })();
    return () => { cancelled = true; };
  }, [enabled, refreshToken, localToken, fanoutKey]);

  const refresh = useCallback(() => setLocalToken((n) => n + 1), []);

  /**
   * 卡片挂哪个 task 版本：taskDetailsById 优先，但要按 updated_at 单单调，
   * 否则一张迟到的扇出快照会把刚点完的状态又盖回去（与 applyTaskDetails
   * 里的 shouldApplyTaskDetail 同一口径 —— 那个护栏管写，这里管读）。
   * 没有更版本时原样返回扇出那份：纯远端任务永远走这条。
   */
  const liveVersion = useCallback(
    (item: WorkspaceTaskItem): WorkspaceTaskItem => {
      const live = getLiveTask(String(item?.task?.id || ""));
      if (!live) return item;
      return String(live.updated_at || "") > String(item.task.updated_at || "")
        ? { ...item, task: live }
        : item;
    },
    [getLiveTask],
  );

  const projects = useMemo<WorkspaceProjectGroup[]>(() => {
    const byProject = new Map<string, WorkspaceTaskItem[]>();
    for (const raw of items) {
      const item = liveVersion(raw);
      const key = scopeKeyForItem(item, getNodeId);
      const bucket = byProject.get(key);
      if (bucket) bucket.push(item);
      else byProject.set(key, [item]);
    }
    return toGroupEntries(managedRootIds, getNodeId, byProject)
      .map(({ rootId, nodeId }) => {
        const key = scopeKey(nodeId, rootId);
        const bucket = (byProject.get(key) || []).slice().sort(byUpdatedDesc);
        return {
          key,
          rootId,
          rootName: getRootDisplayName(rootId) || rootId,
          nodeId,
          color: getNodeColor(rootId) || null,
          tasks: bucket.filter((item) => matchesFilter(item.task, filter)),
        };
      })
      // 没有任务的项目不占地方：筛选后只剩空壳的组、连「全部」下都没有任何任务的项目，
      // 都直接不渲染。空项目要建任务去项目里建，工作台只回答「现在各项目在干什么」。
      .filter((group) => group.tasks.length > 0);
  }, [items, managedRootIds, getNodeId, getRootDisplayName, getNodeColor, filter, liveVersion]);

  return { projects, loading, unreachableNodes, refresh };
}

/** 待审核：等人回话。顶部条带与「待处理」筛选都按这批。 */
export function isBlockedTask(task: KanbanTask): boolean {
  return task.status === "waiting_user" || task.status === "pending";
}

/**
 * 「进行中」的口径是**执行中 + 待审核**，不是「非终态」。
 *
 * 非终态还包含 queued（还没排上）和 paused（手动停的）—— 它们没在跑，也没人等，
 * 混进来之后这个筛选就名不副实了。而一旦选了「进行中」，行里就只该出现这两类状态；
 * 渲染层不再额外补已完成的任务（那属于「全部」）。
 */
function matchesFilter(task: KanbanTask, filter: WorkspaceBoardFilter): boolean {
  if (filter === "active") return task.status === "running" || isBlockedTask(task);
  if (filter === "blocked") return isBlockedTask(task);
  return true;
}
