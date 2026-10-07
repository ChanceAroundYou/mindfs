import type { KanbanTask, TaskDetail } from "./types";
import { appURL } from "../net/base";
import { APIError, protectedJSON } from "../net/api";

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
 * 收尾按钮的四种结局。服务端按**真实状态**分流，前端只负责把结论说清楚：
 *
 * - `session_running`：任务关联的会话正在回复 —— 收尾要拆掉 agent 的 cwd，得等它停。
 *   这是正常等待，不是失败（HTTP 200）。
 * - `teardown`：分支已经在主干里，agent 那半已经做完了 —— 服务端当场把机械清场
 *   （合并 → 拆 worktree → 删分支 → 搬会话）做掉。**这一步是同步的**，回来就已成定局；
 *   失败时服务端回 409 并带上 conflict_files / report，走 APIError。
 * - `nudged`：流水上已有收尾段但还没跑成 —— 重跑那一段催 agent 继续，不追加第二段。
 * - `stage_added`：头一次收尾 —— 追加收尾段并起 agent。
 */
export type BeginFinishAction = "stage_added" | "nudged" | "session_running" | "teardown";

export type BeginFinishResponse = {
  action: BeginFinishAction;
  /** stage_added / nudged 时是「刚改完」的任务快照。 */
  detail?: TaskDetail;
  task?: KanbanTask;
  /** teardown 时的清场结论（成功路径）。 */
  report?: TaskFinishTeardown;
  error?: string;
  conflict_files?: string[];
};

/**
 * 发起收尾。服务端按真实状态分流（见 BeginFinishAction），**幂等** —— 连点几次不会
 * 多出提交、也不会多出收尾段。
 *
 * 与 finishTaskWorktree 的分工：那个是**直接清场**（跳过 agent 阶段，用于 agent 已经
 * 把活提交好的情况），这个先问「该不该让 agent 再动一次」再决定做什么。
 *
 * 非 2xx（清场失败、冲突、worktree 已失效）由 APIError 带出，调用方按普通错误处理。
 */
export async function beginTaskFinishWorktree(rootId: string, taskId: string, nodeId?: string): Promise<BeginFinishResponse> {
  return protectedJSON<BeginFinishResponse>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/begin-finish`, undefined, nodeId), {
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
