import { confirmDialog } from "../../services/dialog";
import type { MessageKey, MessageParams } from "../../i18n/types";
import type { SessionItem } from "./sessionListTypes";

type Translate = (key: MessageKey, params?: MessageParams) => string;

/**
 * 任务分组的三点菜单动作（2026-11-20 用户决策：归档与删除**递归操作全部**，
 * 且要有额外提示）。
 *
 * 为什么单独一层而不是直接复用会话卡的处理器：
 *   - 会话卡归档是一击生效，删除自带一次确认；分组一次性动 N 条会话，需要
 *     **先确认一次再逐条执行**，否则要么弹 N 个框，要么一条都不提示就全删了。
 *   - 确认框里的「{count} 条」只用**组内顶层成员数**：子会话按需懒加载，客户端
 *     手里不一定有完整子树，写一个猜出来的总数反而不诚实。文案明说「及其全部
 *     子会话」—— 后端本来也按 parent_session_key 级联整棵子树。
 *
 * 逐条串行调用而不是 Promise.all：归档/删除都会各自摘本地状态并刷新列表，
 * 并发触发会让同一份列表状态互相覆盖。
 */
function groupTargets(members: readonly SessionItem[]): SessionItem[] {
  return members.filter((member) => Boolean(member.key || member.session_key));
}

/**
 * 整组置顶 / 取消置顶（2026-11-20 用户要求：「置顶按钮不应该移除」）。
 *
 * 置顶是按会话存的（root::sessionKey → 时间戳），分组本身没有条目，所以整组就是
 * 逐条写同一批成员 —— 与归档/删除同一个范围口径。这里**不弹确认**：置顶不像
 * 删除那样不可逆，会话卡的置顶也是一击生效，多一个框只会让常用操作变慢。
 * 逐条串行同理（每次写入都会整表覆盖 pins store，并发写谁最后落谁赢）。
 */
export async function pinTaskGroup(params: {
  members: readonly SessionItem[];
  pinned: boolean;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
}): Promise<void> {
  const { members, pinned, onPin } = params;
  const targets = groupTargets(members);
  if (!onPin || targets.length === 0) return;
  for (const session of targets) {
    await onPin(session, pinned);
  }
}

export async function archiveTaskGroup(params: {
  members: readonly SessionItem[];
  name: string;
  archived: boolean;
  t: Translate;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
}): Promise<void> {
  const { members, name, archived, t, onArchive } = params;
  const targets = groupTargets(members);
  if (!onArchive || targets.length === 0) return;
  const ok = await confirmDialog({
    message: t(
      archived ? "sessionList.confirmArchiveTaskGroup" : "sessionList.confirmUnarchiveTaskGroup",
      { name, count: targets.length },
    ),
  });
  if (!ok) return;
  for (const session of targets) {
    await onArchive(session, archived);
  }
}

export async function deleteTaskGroup(params: {
  members: readonly SessionItem[];
  name: string;
  t: Translate;
  onDelete?: (session: SessionItem, options?: { confirm?: boolean }) => Promise<void> | void;
}): Promise<void> {
  const { members, name, t, onDelete } = params;
  const targets = groupTargets(members);
  if (!onDelete || targets.length === 0) return;
  const ok = await confirmDialog({
    message: t("sessionList.confirmDeleteTaskGroup", { name, count: targets.length }),
    danger: true,
  });
  if (!ok) return;
  // confirm:false —— 上面已经确认过整组，逐条再弹一次就是 N 个框。
  for (const session of targets) {
    await onDelete(session, { confirm: false });
  }
}
