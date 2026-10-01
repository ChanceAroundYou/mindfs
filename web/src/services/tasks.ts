import { appURL } from "./base";
import { getRootNodeId } from "./rootNode";
import { APIError, protectedJSON } from "./api";

export type StageRole = "user" | "agent";
export type TaskStatus =
  | "pending"
  | "queued"
  | "running"
  | "waiting_user"
  | "paused"
  | "success"
  | "fail"
  | "cancelled";

export type StageTemplate = {
  id?: string;
  name: string;
  role: StageRole;
  auto_advance?: boolean;
  /** 首段（任务输入）专用：建完立刻开跑，不用等用户点「立即执行」 */
  start_immediately?: boolean;
  agent?: string;
  model?: string;
  mode?: string;
  effort?: string;
  fast_service?: string;
  plan_mode?: boolean;
  session_reuse_policy?: "task_main" | "same_stage" | "always_new";
  prompt_template?: string;
  agent_can_control_stage?: boolean;
  /**
   * 服务端生成的段标记（"" = 普通段，"worktree_finish" = 收尾段）。
   *
   * **必须在这里带上**：updateTaskStage 发的是整个 stage 对象，服务端那边是整段替换
   * （UpdateStage）。类型里没有这个字段的话，用户在面板上编辑一次收尾段，标记就被
   * 静默抹掉，那个任务再也认不出自己走过收尾流程。
   */
  kind?: string;
  created_at?: string;
  updated_at?: string;
};

export type TaskTemplateStage = {
  id?: string;
  stage_template_id?: string;
  position: number;
  snapshot: StageTemplate;
};

export type TaskTemplate = {
  id?: string;
  name: string;
  description?: string;
  stages: TaskTemplateStage[];
  created_at?: string;
  updated_at?: string;
};

export type KanbanTask = {
  id: string;
  task_number?: number;
  root_id: string;
  name?: string;
  task_template_id: string;
  task_template_name: string;
  stages?: StageTemplate[];
  create_worktree?: boolean;
  worktree_branch_mode?: "new" | "existing";
  worktree_branch?: string;
  current_stage_index: number;
  status: TaskStatus;
  scheduler_admitted?: boolean;
  main_session_key?: string;
  worktree_root_id?: string;
  worktree_path?: string;
  /** 派生（服务端算，不落库）：worktree_path 指向的目录已经不在了。 */
  worktree_missing?: boolean;
  /**
   * 这个任务**曾经建出过一个 worktree**（服务端落库，清归属时不清它）。
   *
   * 为什么必须要：worktree_path 为空有两种相反的含义 ——
   * 「还没建」（首段还是 user 段）和「建过、记录被清掉了」。只有这一个字段能
   * 把两者分开。没有它，「路径为空」会被一律显示成「已收尾」—— 2026-10-01 实测
   * 就是这样：一个仍在使用的 worktree 被标成了活已经并回主干。
   */
  worktree_built?: boolean;
  labels?: string[];
  created_at: string;
  updated_at: string;
  completed_at?: string;
  current_stage_name?: string;
  current_stage_status?: string;
  aux_flags?: {
    ask_user_waiting?: boolean;
    has_plan?: boolean;
    has_todos?: boolean;
    has_task?: boolean;
    session_error?: string;
  };
};

export type StageRun = {
  id: string;
  task_id: string;
  stage_index: number;
  stage_name: string;
  role: StageRole;
  status: string;
  session_key?: string;
  input?: string;
  rendered_prompt?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
};

export type TaskEvent = {
  id: string;
  task_id: string;
  stage_run_id?: string;
  type: string;
  payload_json?: string;
  created_at: string;
};

export type TaskDetail = {
  task: KanbanTask;
  stage_runs: StageRun[];
  events: TaskEvent[];
};

type CachedTaskRecord = {
  cacheKey: string;
  rootId: string;
  taskId: string;
  updatedAt: string;
  detail: TaskDetail;
};

type CachedTaskMeta = {
  key: string;
  rootId: string;
  newestUpdatedAt: string;
  oldestUpdatedAt: string;
  lastSyncedAt: string;
};

const TASK_CACHE_DB = "mindfs-task-cache";
const TASK_CACHE_VERSION = 1;
const TASK_STORE = "tasks";
const TASK_META_STORE = "meta";

function taskCacheKey(rootId: string, taskId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  return nid ? `${nid}::${rootId}::${taskId}` : `${rootId}::${taskId}`;
}

function taskMetaKey(rootId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  return nid ? `${nid}::root::${rootId}` : `root::${rootId}`;
}


function taskRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("indexeddb request failed"));
  });
}

function openTaskCacheDB(): Promise<IDBDatabase> {
  if (typeof window === "undefined" || !("indexedDB" in window)) {
    return Promise.reject(new Error("indexeddb unavailable"));
  }
  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(TASK_CACHE_DB, TASK_CACHE_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(TASK_STORE)) {
        const store = db.createObjectStore(TASK_STORE, { keyPath: "cacheKey" });
        store.createIndex("rootId", "rootId", { unique: false });
        store.createIndex("updatedAt", "updatedAt", { unique: false });
      }
      if (!db.objectStoreNames.contains(TASK_META_STORE)) {
        db.createObjectStore(TASK_META_STORE, { keyPath: "key" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("indexeddb open failed"));
  });
}

async function withTaskStore<T>(
  mode: IDBTransactionMode,
  fn: (stores: { tasks: IDBObjectStore; meta: IDBObjectStore }) => Promise<T> | T,
): Promise<T> {
  const db = await openTaskCacheDB();
  try {
    const tx = db.transaction([TASK_STORE, TASK_META_STORE], mode);
    const result = await fn({
      tasks: tx.objectStore(TASK_STORE),
      meta: tx.objectStore(TASK_META_STORE),
    });
    await new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error || new Error("indexeddb transaction failed"));
      tx.onabort = () => reject(tx.error || new Error("indexeddb transaction aborted"));
    });
    return result;
  } finally {
    db.close();
  }
}

export async function getCachedTaskDetails(rootId: string, nodeId?: string): Promise<TaskDetail[]> {
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readonly", async ({ tasks }) => {
      const index = tasks.index("rootId");
      const records = await taskRequest(index.getAll(rootId) as IDBRequest<CachedTaskRecord[]>);
      if (nid) {
        const scoped = records.filter((r) => String(r.cacheKey || "").startsWith(`${nid}::`));
        if (scoped.length > 0) {
          const byId = new Map<string, CachedTaskRecord>();
          for (const rec of scoped) {
            const prev = byId.get(rec.taskId);
            if (!prev || String(rec.updatedAt || "") > String(prev.updatedAt || "")) byId.set(rec.taskId, rec);
          }
          return Array.from(byId.values())
            .map((record) => record.detail)
            .filter((detail) => detail?.task?.id)
            .sort((a, b) => String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || "")));
        }
        return [];
      }
      const byId = new Map<string, CachedTaskRecord>();
      for (const rec of records) {
        const prev = byId.get(rec.taskId);
        if (!prev || String(rec.updatedAt || "") > String(prev.updatedAt || "")) byId.set(rec.taskId, rec);
      }
      return Array.from(byId.values())
        .map((record) => record.detail)
        .filter((detail) => detail?.task?.id)
        .sort((a, b) => String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || "")));
    });
  } catch {
    return [];
  }
}

export async function getCachedTaskMeta(rootId: string, nodeId?: string): Promise<CachedTaskMeta | null> {
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readonly", async ({ meta }) => {
      if (nid) {
        const scoped = await taskRequest(meta.get(taskMetaKey(rootId, nid)) as IDBRequest<CachedTaskMeta | undefined>);
        if (scoped) return scoped;
      }
      const value = await taskRequest(meta.get(taskMetaKey(rootId)) as IDBRequest<CachedTaskMeta | undefined>);
      return value || null;
    });
  } catch {
    return null;
  }
}

export async function upsertCachedTaskDetails(rootId: string, details: TaskDetail[], nodeId?: string): Promise<void> {
  const valid = details.filter((detail) => detail?.task?.id);
  if (valid.length === 0) return;
  const nid = String(nodeId || "").trim();
  try {
    await withTaskStore("readwrite", async ({ tasks, meta }) => {
      const metaKey = taskMetaKey(rootId, nid || undefined);
      let currentMeta = await taskRequest(meta.get(metaKey) as IDBRequest<CachedTaskMeta | undefined>);
      const updatedValues = valid
        .map((detail) => String(detail.task.updated_at || ""))
        .filter(Boolean);
      for (const detail of valid) {
        const taskId = detail.task.id;
        await taskRequest(tasks.put({
          cacheKey: taskCacheKey(rootId, taskId, nid || undefined),
          rootId,
          taskId,
          updatedAt: String(detail.task.updated_at || ""),
          detail,
        } satisfies CachedTaskRecord));
      }
      const newest = updatedValues.reduce((max, value) => value > max ? value : max, currentMeta?.newestUpdatedAt || "");
      const oldest = updatedValues.reduce((min, value) => !min || value < min ? value : min, currentMeta?.oldestUpdatedAt || "");
      currentMeta = {
        key: metaKey,
        rootId,
        newestUpdatedAt: newest,
        oldestUpdatedAt: oldest,
        lastSyncedAt: new Date().toISOString(),
      };
      await taskRequest(meta.put(currentMeta));
    });
  } catch {}
}

/**
 * 淘汰「服务端已经没有、缓存里还留着」的任务。
 *
 * 缓存以前只 put 不 delete，配合增量拉取（after=newestUpdatedAt，只取更新的行）
 * 就等于把删除永久吞掉：任务在服务端消失后，响应里永远不会出现它，缓存就一直
 * 喂给你，跨设备还各存各的。实测手机上看得到 #11/#13，而两台机器的库里都查不到。
 *
 * keepTaskIds 必须是**全量**响应的集合（不带 after / limit，服务端会返回该 root
 * 的全部任务，见 task_store.go:275）。拿增量或限流响应当权威集合会误删。
 */
export async function pruneCachedTaskDetails(rootId: string, keepTaskIds: Iterable<string>, nodeId?: string): Promise<string[]> {
  const keep = new Set(Array.from(keepTaskIds, (id) => String(id || "")).filter(Boolean));
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readwrite", async ({ tasks }) => {
      const index = tasks.index("rootId");
      const records = await taskRequest(index.getAll(rootId) as IDBRequest<CachedTaskRecord[]>);
      // 按 root + node 圈定：同名项目跨节点时各存各的，淘汰不能波及另一台机器。
      const scoped = nid ? records.filter((r) => String(r.cacheKey || "").startsWith(`${nid}::`)) : records;
      const dropped: string[] = [];
      for (const rec of scoped) {
        if (keep.has(String(rec.taskId || ""))) continue;
        dropped.push(String(rec.taskId || ""));
        await taskRequest(tasks.delete(rec.cacheKey));
      }
      return dropped;
    });
  } catch {
    return [];
  }
}

export async function fetchStageTemplates(nodeId?: string): Promise<StageTemplate[]> {
  const payload = await protectedJSON<any>(appURL("/api/task-stage-templates", undefined, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function saveStageTemplate(template: StageTemplate, nodeId?: string): Promise<StageTemplate> {
  return protectedJSON<StageTemplate>(appURL("/api/task-stage-templates", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(template),
  });
}

export async function deleteStageTemplate(id: string, nodeId?: string): Promise<void> {
  await protectedJSON(appURL(`/api/task-stage-templates/${encodeURIComponent(id)}`, undefined, nodeId), {
    method: "DELETE",
  });
}

export async function fetchTaskTemplates(nodeId?: string): Promise<TaskTemplate[]> {
  const payload = await protectedJSON<any>(appURL("/api/task-templates", undefined, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function saveTaskTemplate(template: TaskTemplate, nodeId?: string): Promise<TaskTemplate> {
  const path = template.id ? `/api/task-templates/${encodeURIComponent(template.id)}` : "/api/task-templates";
  return protectedJSON<TaskTemplate>(appURL(path, undefined, nodeId), {
    method: template.id ? "PUT" : "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(template),
  });
}

export async function deleteTaskTemplate(id: string, nodeId?: string): Promise<void> {
  await protectedJSON(appURL(`/api/task-templates/${encodeURIComponent(id)}`, undefined, nodeId), {
    method: "DELETE",
  });
}

export async function fetchTaskDetails(rootId: string, filters?: {
  templateId?: string;
  status?: string;
  stage?: number;
  after?: string;
  before?: string;
  limit?: number;
  /** 项目内的任务号（每项目自增）。命中即一条 —— 工作台就地开详情就走这条 */
  taskNumber?: number;
}, nodeId?: string): Promise<TaskDetail[]> {
  const params = new URLSearchParams({ root: rootId });
  if (filters?.templateId) params.set("template_id", filters.templateId);
  if (filters?.status) params.set("status", filters.status);
  if (typeof filters?.stage === "number") params.set("stage", String(filters.stage));
  if (filters?.after) params.set("after", filters.after);
  if (filters?.before) params.set("before", filters.before);
  if (typeof filters?.limit === "number" && filters.limit > 0) params.set("limit", String(filters.limit));
  if (typeof filters?.taskNumber === "number" && filters.taskNumber > 0) params.set("task_number", String(filters.taskNumber));
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/tasks", params, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function createTask(
  rootId: string,
  taskTemplateId: string,
  input: string,
  createWorktree = false,
  worktreeBranchMode: "new" | "existing" = "new",
  worktreeBranch = "",
  nodeId?: string,
  options?: { name?: string; stages?: StageTemplate[] },
): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL("/api/tasks", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      root_id: rootId,
      task_template_id: taskTemplateId,
      input,
      create_worktree: createWorktree,
      worktree_branch_mode: worktreeBranchMode,
      worktree_branch: worktreeBranch,
      ...(options?.name ? { name: options.name } : {}),
      ...(options?.stages?.length ? { stages: options.stages } : {}),
    }),
  });
}

export async function updateTaskInput(
  rootId: string,
  taskId: string,
  input: string,
  createWorktree?: boolean,
  worktreeBranchMode?: "new" | "existing",
  worktreeBranch?: string,
  nodeId?: string,
): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/input`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      root_id: rootId,
      input,
      ...(typeof createWorktree === "boolean" ? { create_worktree: createWorktree } : {}),
      ...(worktreeBranchMode ? { worktree_branch_mode: worktreeBranchMode } : {}),
      ...(typeof worktreeBranch === "string" ? { worktree_branch: worktreeBranch } : {}),
    }),
  });
}

export async function moveTask(rootId: string, taskId: string, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "fail", reason = "", nodeId?: string): Promise<TaskDetail> {
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/${action}`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, reason }),
  });
}

/**
 * 重建已删除的任务 worktree。
 *
 * 显式动作，服务端不自动重建：目录没了之后那个分支可能还在（代码还在分支上）也可能已经
 * 跟着删了，自动重建等于替用户做这个决定。失败（如 branch already exists）已经把原因
 * 记到任务的 session_error 上，所以这里照样 resolve，由调用方刷详情看那条错误。
 */
export async function rebuildTaskWorktree(rootId: string, taskId: string, nodeId?: string): Promise<TaskDetail> {
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/rebuild-worktree`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId }),
  });
}

/**
 * 一次 worktree 收尾的结果。
 *
 * 字段都是「实际发生了什么」而不是「我们请求了什么」：分支删不掉（未合并）时
 * BranchDeleted=false 且 BranchSkipReason 里有 git 的原话，合并撞上冲突时抛
 * FinishWorktreeConflict。所以 UI 能分别渲染「已完成 / 卡在哪一步 / 要人工解冲突」。
 */
export type FinishWorktreeResult = {
  task: KanbanTask;
  /** false = 源分支本来就在目标分支里（重复收尾），不是失败。 */
  merged: boolean;
  commit?: string;
  /** false = worktree 目录本来就不在了（已拆过或从没建成），不是失败。 */
  worktree_removed: boolean;
  /** false = 没请求删分支、未合并被 git 拒绝、或分支已不存在。 */
  branch_deleted: boolean;
  /** BranchDeleted=false 的人话原因（不靠猜）。 */
  branch_skip_reason?: string;
  /** .worktree/ 下的残留目录，只列不删。 */
  orphans?: Array<{ path: string; non_empty: boolean; files?: string[] }>;
};

/**
 * 合并撞上冲突。**不是普通错误**：仓库现在停在 MERGE_HEAD，需要人工处理，
 * 所以文件清单要单独拿出来给 UI 列出可点的条目，不能埋在 message 里。
 *
 * 服务端回 409 + { error, conflict_files, output, result }。
 */
export class FinishWorktreeConflict extends Error {
  readonly conflictFiles: string[];
  readonly output: string;
  constructor(message: string, conflictFiles: string[] = [], output = "") {
    super(message);
    this.name = "FinishWorktreeConflict";
    this.conflictFiles = conflictFiles;
    this.output = output;
  }
}

/**
 * 收尾任务在 worktree 里的活：合回主 checkout → 拆 worktree → 删分支 → 列残留。
 *
 * 撞上冲突时 reject 一个 FinishWorktreeConflict（带文件清单）；其它失败是普通
 * Error。两条路都要分开处理：前者要引导用户手工解冲突，后者只是报一句。
 */
export async function finishTaskWorktree(
  rootId: string,
  taskId: string,
  opts: { target?: string; deleteBranch?: boolean; pruneOrphans?: boolean; nodeId?: string } = {},
): Promise<FinishWorktreeResult> {
  const { target, deleteBranch = true, pruneOrphans = true, nodeId } = opts;
  try {
    // 走 protectedJSON 而不是裸 fetch：E2EE 封装和「本机账户被删 → 登出」都在里面，
    // 绕过去就丢了这两条。409 撞上冲突时它抛 APIError（带 status 和 payload），
    // 正好够下面还原成 FinishWorktreeConflict。
    return await protectedJSON<FinishWorktreeResult>(
      appURL(`/api/tasks/${encodeURIComponent(taskId)}/finish-worktree`, undefined, nodeId),
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ root_id: rootId, target, delete_branch: deleteBranch, prune_orphans: pruneOrphans }),
      },
    );
  } catch (error) {
    if (error instanceof APIError && error.status === 409) {
      const files = Array.isArray(error.payload?.conflict_files) ? error.payload.conflict_files.map(String) : [];
      throw new FinishWorktreeConflict(error.message, files, String(error.payload?.output || ""));
    }
    throw error;
  }
}

/**
 * 发起收尾流程：追加一段收尾阶段并起 agent，由它自己去 commit + merge。
 *
 * 与 finishTaskWorktree 的分工：那个是**直接清场**（跳过 agent 阶段，用于
 * agent 已经把活提交好的情况），这个是**走完整流程** —— 清场发生在收尾段成功
 * **之后**，由服务端自己接着做（拆目录 → 删分支 → 搬会话），前端不用等。
 *
 * 所以这里返回的 detail 是「刚刚追加了收尾段」那一刻的快照，worktree_path 还在。
 * 服务端 409（正在执行 / 已在收尾 / 目录已失效）由 APIError 带出，调用方按
 * 普通错误处理即可。
 */
export async function beginTaskFinishWorktree(rootId: string, taskId: string, nodeId?: string): Promise<TaskDetail> {
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/begin-finish`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId }),
  });
}

/**
 * 收尾段跑完之后服务端清场的结论（WS `task.finish_teardown`）。
 *
 * 前端在 beginTaskFinishWorktree 之后就撒手了：清场是服务端自己的 goroutine 在跑，
 * 没有任何 HTTP 响应会回来告诉用户成没成。这一条推送就是那个回执。
 *
 * 字段与 FinishWorktreeResult 同源，error/conflict_files 沿用 finish-worktree
 * 的口径：409 类冲突 = 仓库停在 MERGE_HEAD 等人处理（要列文件），其它错只给一句。
 */
export type TaskFinishTeardown = {
  root_id: string;
  task_id: string;
  result?: FinishWorktreeResult;
  conflict_files?: string[];
  error?: string;
  session_note?: string;
  session_warning?: string;
};

export type TaskOverviewItem = {
  root_id: string;
  root_name: string;
  task: KanbanTask;
  /**
   * 前端跨节点扇出时打的标，**后端不返**（后端 Overview 遍历本节点 roots，不知道自己在哪个节点上）。
   * 可选是为了让直接用 /api/tasks/overview 的调用方类型照旧成立。
   */
  nodeId?: string;
};

export async function fetchTasksOverview(nodeId?: string): Promise<TaskOverviewItem[]> {
  const payload = await protectedJSON<any>(appURL("/api/tasks/overview", undefined, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function renameTask(rootId: string, taskId: string, name: string, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/rename`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, name }),
  });
}

export async function addTaskStage(rootId: string, taskId: string, stage: StageTemplate, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/add-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, stage }),
  });
}

export async function updateTaskStage(rootId: string, taskId: string, index: number, stage: StageTemplate, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/update-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, index, stage }),
  });
}

export async function removeTaskStage(rootId: string, taskId: string, index: number, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/remove-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, index }),
  });
}
