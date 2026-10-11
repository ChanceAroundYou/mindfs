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
  /**
   * 目录本体（含 git 不跟踪的 .mindfs/ .omc/ .claude/）有没有被整个删掉。
   * 收尾的语义是「这个目录不再需要」，所以拆完 git 的 worktree 之后还会把目录删干净。
   */
  worktree_dir_removed?: boolean;
  /** 目录没删掉的人话原因（不靠猜）。 */
  dir_remove_reason?: string;
  /**
   * 顺手删掉的空壳孤儿目录（里面只剩 .mindfs/ .omc/ .claude/）。
   * 报出来是为了让「它删了什么」可见。
   */
  removed_orphans?: string[];
  /**
   * 从 worktree 的 .mindfs/upload/ 搬回主 checkout 的附件（相对 upload/ 的路径）。
   * 拆目录前必须搬，否则用户在 worktree 会话里传的文件会跟着消失。
   */
  migrated_uploads?: string[];
  /**
   * .worktree/ 下**还留着**的残留目录。只剩工具状态目录的空壳会被自动删掉
   * （见 removed_orphans），剩下的只列不删 —— 里面可能有别的任务正在用的东西。
   */
  orphans?: Array<{ path: string; non_empty: boolean; files?: string[] }>;
};

/**
 * 「这次收尾该走哪条路」的服务端只读判定。
 *
 * 收尾是 agent + 机械清场两个阶段的整合，但很多情况下 agent 那半已经没活可干了
 * （分支早合进主干、worktree 也干净），再跑一遍只会多一个空提交、让用户白等一轮。
 * 服务端先做一次只读判定，能机械清场就直接做掉。
 *
 * mechanical=false 时 reason/files 说明**为什么**要交给 agent（worktree 有没提交的
 * 改动 / 合并会冲突），UI 据此把理由说清楚，而不是让用户猜为什么又跑起了 agent。
 */
export type FinishPlan = {
  mechanical: boolean;
  /** mechanical=false 的原因（短句）。 */
  reason?: string;
  /** 相关文件清单（未提交的文件 / 会冲突的文件）。 */
  files?: string[];
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
 * 主 checkout 有未提交改动，git 不敢替用户合并。**不是普通错误**：那不是「收尾
 * 失败了」，是「你自己的活还没收」—— 下一步是提交或暂存，不是重试。
 *
 * 与 FinishWorktreeConflict 并列而不是共用一个类型：冲突要列文件让用户去解，
 * 这个是让用户先处理自己的改动。混成一个类型的话 UI 只能靠猜来分流。
 *
 * 服务端回 409 + { error, dirty_files, result }。
 */
export class FinishWorktreeDirty extends Error {
  readonly files: string[];
  constructor(message: string, files: string[] = []) {
    super(message);
    this.name = "FinishWorktreeDirty";
    this.files = files;
  }
}

/**
 * 合并**已经成功**、但 worktree 目录里还有用户没提交的改动，git 拒绝拆目录。
 *
 * 与 FinishWorktreeDirty 并列：那个是主 checkout 不干净、合并还没做；这个是 worktree
 * 自己不干净、合并已经做完了。下一步一样（先提交/暂存），但「活合了没有」是两回事，
 * 所以分开报 —— 用户看到前者会以为白干了一场，看到后者知道代码已经在主干里。
 *
 * 服务端回 409 + { error, user_changes, result }。
 */
export class FinishWorktreeUserChanges extends Error {
  readonly files: string[];
  constructor(message: string, files: string[] = []) {
    super(message);
    this.name = "FinishWorktreeUserChanges";
    this.files = files;
  }
}

/**
 * 收尾任务在 worktree 里的活：合回主 checkout → 拆 worktree → 删分支 → 列残留。
 *
 * 撞上冲突时 reject 一个 FinishWorktreeConflict（带文件清单）；主 checkout 不干净时
 * reject 一个 FinishWorktreeDirty（带文件清单）；worktree 自己不干净时 reject 一个
 * FinishWorktreeUserChanges（带文件清单）；其它失败是普通 Error。四条路都要分开处理：
 * 冲突要引导用户手工解冲突，另两个要引导用户先提交/暂存，后者只是报一句。
 */
export async function finishTaskWorktree(
  rootId: string,
  taskId: string,
  opts: { target?: string; deleteBranch?: boolean; pruneOrphans?: boolean; nodeId?: string } = {},
): Promise<FinishWorktreeResult> {
  const { target, deleteBranch = true, pruneOrphans = true, nodeId } = opts;
  try {
    // 走 protectedJSON 而不是裸 fetch：「本机账户被删 → 登出」和跨节点 deadline 都在里面，
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
      const conflictFiles = Array.isArray(error.payload?.conflict_files) ? error.payload.conflict_files.map(String) : [];
      if (conflictFiles.length > 0) {
        throw new FinishWorktreeConflict(error.message, conflictFiles, String(error.payload?.output || ""));
      }
      const dirtyFiles = Array.isArray(error.payload?.dirty_files) ? error.payload.dirty_files.map(String) : [];
      if (dirtyFiles.length > 0) {
        throw new FinishWorktreeDirty(error.message, dirtyFiles);
      }
      const userChanges = Array.isArray(error.payload?.user_changes) ? error.payload.user_changes.map(String) : [];
      if (userChanges.length > 0) {
        throw new FinishWorktreeUserChanges(error.message, userChanges);
      }
    }
    throw error;
  }
}

/**
 * 收尾按钮的四种结局。服务端按**真实状态**分流，前端只负责把结论说清楚：
 *
 * - `session_running`：任务关联的会话正在回复 —— 收尾要拆掉 agent 的 cwd，得等它停。
 *   这是正常等待，不是失败（HTTP 200）。
 * - `teardown`：**机械优先** —— 服务端只读判定认为机械清场能直接做完（分支已合进
 *   主干 / worktree 干净且合并无冲突），于是当场把机械清场（合并 → 拆 worktree →
 *   删目录 → 删分支 → 搬会话）做掉，不等 agent。**这一步是同步的**，回来就已成定局；
 *   失败时服务端回 409 并带上 conflict_files / report，走 APIError。
 * - `nudged`：机械清场做不了、而流水上已有收尾段 —— 重跑那一段催 agent 继续，
 *   不追加第二段。
 * - `stage_added`：机械清场做不了、也没有收尾段 —— 追加收尾段并起 agent，让 agent
 *   先把活提交并合回主干。段成功之后服务端钩子接着做机械清场。
 *
 * `plan` 字段说明这次判定为什么走这条路（mechanical + reason + files）。
 */
export type BeginFinishAction = "stage_added" | "nudged" | "session_running" | "teardown";

export type BeginFinishResponse = {
  action: BeginFinishAction;
  /**
   * 服务端的只读判定：这次为什么走机械清场、或者为什么要交给 agent。
   * stage_added / nudged / teardown 三条路都会带上。
   */
  plan?: FinishPlan;
  /** stage_added / nudged 时是「刚改完」的任务快照。 */
  detail?: TaskDetail;
  task?: KanbanTask;
  /** teardown 时的清场结论（成功路径）。 */
  report?: TaskFinishTeardown;
  error?: string;
  conflict_files?: string[];
  /** teardown 失败时：主 checkout 里那些没提交的路径。 */
  dirty_files?: string[];
  /** teardown 失败时：worktree 里那些没提交的路径（合并已成功、只是目录拆不掉）。 */
  user_changes?: string[];
};

/**
 * 发起收尾。服务端按真实状态分流（见 BeginFinishAction），**幂等** —— 连点几次不会
 * 多出提交、也不会多出收尾段。
 *
 * 与 finishTaskWorktree 的分工：那个是**直接清场**（跳过 agent 阶段，用于 agent 已经
 * 把活提交好的情况），这个先问「该不该让 agent 再动一次」再决定做什么。
 *
 * 非 2xx（清场失败、冲突、worktree 已失效）由 APIError 带出。
 *
 * **409 在这里就还原成类型化错误**（与 finishTaskWorktree 同一套）：teardown 是
 * ③.5 分支（流水里没有 agent 段时直接机械清场）同步做的，它的 409 同样带
 * conflict_files / dirty_files / user_changes。不还原的话调用方只能拿到一句
 * APIError，文件清单整个丢掉 —— 而「清单进弹窗、句子保持短」正是这一轮的要求。
 */
export async function beginTaskFinishWorktree(rootId: string, taskId: string, nodeId?: string): Promise<BeginFinishResponse> {
  try {
    return await protectedJSON<BeginFinishResponse>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/begin-finish`, undefined, nodeId), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ root_id: rootId }),
    });
  } catch (error) {
    if (error instanceof APIError && error.status === 409) {
      const conflictFiles = Array.isArray(error.payload?.conflict_files) ? error.payload.conflict_files.map(String) : [];
      if (conflictFiles.length > 0) {
        throw new FinishWorktreeConflict(error.message, conflictFiles, String(error.payload?.output || ""));
      }
      const dirtyFiles = Array.isArray(error.payload?.dirty_files) ? error.payload.dirty_files.map(String) : [];
      if (dirtyFiles.length > 0) {
        throw new FinishWorktreeDirty(error.message, dirtyFiles);
      }
      const userChanges = Array.isArray(error.payload?.user_changes) ? error.payload.user_changes.map(String) : [];
      if (userChanges.length > 0) {
        throw new FinishWorktreeUserChanges(error.message, userChanges);
      }
    }
    throw error;
  }
}

/**
 * 收尾段跑完之后服务端清场的结论（WS `task.finish_teardown`）。
 *
 * 前端在 beginTaskFinishWorktree 之后就撒手了：清场是服务端自己的 goroutine 在跑，
 * 没有任何 HTTP 响应会回来告诉用户成没成。这一条推送就是那个回执。
 *
 * 字段与 FinishWorktreeResult 同源，error/conflict_files/dirty_files 沿用
 * finish-worktree 的口径：409 类冲突 = 仓库停在 MERGE_HEAD 等人处理（要列文件），
 * dirty_files = 主 checkout 有未提交改动（要先提交/暂存），其它错只给一句。
 */
export type TaskFinishTeardown = {
  root_id: string;
  task_id: string;
  result?: FinishWorktreeResult;
  conflict_files?: string[];
  dirty_files?: string[];
  user_changes?: string[];
  error?: string;
  /**
   * 「这次没做什么、为什么」的说明（幂等跳过等）。与 error 分开：note 非空但
   * error 为空 = 一切正常，只是本来就没活可干。
   */
  note?: string;
  session_note?: string;
  session_warning?: string;
};
