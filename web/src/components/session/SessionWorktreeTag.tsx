import { useI18n } from "../../i18n/index";
import { taskWorktreeTagStyle } from "../task/taskIcons";
import type { RelatedWorktree } from "../../shared/sessionTypes";

/**
 * 会话行的绿 worktree 标签（G-AD）：只在**有活树**时出现，悬在会话名上方、
 * 与名字左对齐（用户 2026-10-16 定）。
 *
 * 绝对定位、不占流：无论标签出不出现，会话名与左右两侧图标的位置一格都不动。
 * 定位基准是 SessionCard 里那个 position:relative 的按钮（= 名字区），
 * 所以 left:0 就是「名字的左边缘」，bottom:100% 就是「名字的上方」。
 *
 * 无 worktree 与已收尾一律不渲染：「没有树」不是会话进展，每行挂个灰标签只是噪声。
 * 任务卡那套三态（enabled/finished/none）在会话行只留 enabled —— 判据见
 * isWorktreeActive：path 在且目录没被删才算活树。
 *
 * 样式复用任务卡的 taskWorktreeTagStyle（会话域引用任务域图标的先例见
 * SessionViewer* 引 stream 图标），不自己画第二套。
 */
export function SessionWorktreeTag({
  relatedWorktree,
  worktreeMissing,
}: {
  relatedWorktree?: RelatedWorktree | null;
  worktreeMissing?: boolean;
}) {
  const { t } = useI18n();
  if (!isWorktreeActive(relatedWorktree, worktreeMissing)) {
    return null;
  }
  return (
    <span
      title={t("task.worktreeTitle")}
      aria-label={t("task.worktreeTitle")}
      style={{
        ...taskWorktreeTagStyle("enabled"),
        position: "absolute",
        left: 0,
        bottom: "100%",
        marginLeft: 0,
      }}
    >
      worktree
    </span>
  );
}

// 会话没有任务的 worktree_built 位：path 在且目录没被删 = 有活树；
// path 还在但目录已删 = 已收尾；本来就没 path = 没开过。
function isWorktreeActive(
  relatedWorktree?: RelatedWorktree | null,
  worktreeMissing?: boolean,
): boolean {
  const path = String(relatedWorktree?.path || "").trim();
  return Boolean(path) && !worktreeMissing;
}
