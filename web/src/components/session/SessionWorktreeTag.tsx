import { useI18n } from "../../i18n/index";
import { taskWorktreeTagStyle } from "../task/taskIcons";

/**
 * 会话行的绿 worktree 标签（G-AD）：只在**有活树**时出现。
 *
 * 位置由外层 SessionCardTopBadges 决定（整排悬在会话名上方、与名字左对齐，用户 2026-10-16 定）——
 * 本组件只负责「长什么样、什么时候显示」，不再自己绝对定位。
 *
 * 无 worktree 与已收尾一律不渲染：「没有树」不是会话进展，每行挂个灰标签只是噪声。
 * 任务卡那套三态（enabled/finished/none）在会话行只留 enabled。
 *
 * 样式复用任务卡的 taskWorktreeTagStyle（会话域引用任务域图标的先例见
 * SessionViewer* 引 stream 图标），不自己画第二套。
 */
export function SessionWorktreeTag({ worktreeActive }: { worktreeActive?: boolean }) {
  const { t } = useI18n();
  if (!isWorktreeActive(worktreeActive)) {
    return null;
  }
  return (
    <span
      title={t("task.worktreeTitle")}
      aria-label={t("task.worktreeTitle")}
      style={{
        ...taskWorktreeTagStyle("enabled"),
        // 任务卡的标签靠 marginLeft:auto 顶到行尾；在上方这排里它排第一，
        // 留着 auto 会把后面的徽标一起推到右边去。
        marginLeft: 0,
        // 整排容器是 pointerEvents:none（悬在上一行留白上，不该挡点击），
        // 标签自己要收回来，否则 title 提示不出来。
        pointerEvents: "auto",
      }}
    >
      worktree
    </span>
  );
}

/**
 * 活树判据只有后端能给：前端 stat 不了文件系统。worktree_active = 路径在 + 目录还在
 * + 它**确实是** linked worktree（`.git` 是文件）。导出给 SessionCardTopBadges 的
 * hasSessionTopBadges 共用 —— 判据只能有一处。
 *
 * 别退回「related_worktree.path 非空 && !worktree_missing」：那只问「目录还在不在」，
 * 而主 checkout 的目录当然一直在。2026-10-16 实测 14 条归属里 13 条因此误亮
 * （本项目主 checkout、别的仓库的主 checkout、乃至不是仓库的 ~/.claude）。
 */
export function isWorktreeActive(worktreeActive?: boolean): boolean {
  return worktreeActive === true;
}
