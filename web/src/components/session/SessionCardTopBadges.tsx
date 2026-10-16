import { useI18n } from "../../i18n/index";
import { renderToolIcon } from "../stream/toolCallIcons";
import { taskAuxBadgeStyle, taskAuxIconScaleStyle } from "../task/taskIcons";
import { SessionStatusBadges, hasSessionStatusBadges } from "./SessionStatusBadges";
import { SessionWorktreeTag, isWorktreeActive } from "./SessionWorktreeTag";
import type { SessionItem } from "./sessionListTypes";

/**
 * 会话名上方那排状态徽标（G-AD）：worktree → 问号 → error/plan/todo/task。
 *
 * 子代理的折角是它的常驻类型图标，固定留在名字前（SessionCardIcon）；子代理不渲染
 * 任何状态徽标，所以不进入本组件，也不触发选中底色向上拓展。
 * 定位基准是 SessionCard 里那个 position:relative 的名字按钮，所以 left:0 就是
 * 「名字的左边缘」。**这一排是唯一的上方徽标来源** —— SessionCard 的选中底色靠
 * hasSessionTopBadges 决定要不要向上拓展来包住它，两边必须用同一个判据。
 *
 * 排空时整块不渲染（return null）：没徽标却留一个空盒子，等于白占一行。
 *
 * 高度上限：这排最多 14px（worktree 标签与 aux 徽标同高），而上下两行之间
 * 有 20px 留白（盒内 padding 7 + 行 padding 2 + 列表 gap 2 + 行 padding 2 + 盒内 padding 7），
 * 所以它只吃掉上一个会话留白里的一小截，不会压到上一行的文字。
 */
export function SessionCardTopBadges({
  session,
  isSubagent,
  nodeColor,
}: {
  session: SessionItem;
  isSubagent: boolean;
  nodeColor?: string;
}) {
  const { t } = useI18n();
  if (isSubagent || !hasSessionTopBadges(session, isSubagent)) {
    return null;
  }
  return (
    <span
      style={{
        position: "absolute",
        left: 0,
        bottom: "100%",
        display: "inline-flex",
        alignItems: "center",
        gap: "3px",
        whiteSpace: "nowrap",
        pointerEvents: "none",
      }}
    >
      <SessionWorktreeTag worktreeActive={session.worktree_active} />
      {session.aux_flags?.ask_user_waiting ? (
        <span
          aria-label={t("sessionList.awaitingUser")}
          title={t("sessionList.awaitingUser")}
          style={{ ...taskAuxBadgeStyle(true), pointerEvents: "auto" }}
        >
          <span style={taskAuxIconScaleStyle()}>{renderToolIcon("ask_user")}</span>
        </span>
      ) : null}
      <SessionStatusBadges auxFlags={session.aux_flags} nodeColor={nodeColor} />
    </span>
  );
}

/**
 * 「这行上方有没有徽标」—— SessionCard 决定选中底色要不要向上拓展的唯一判据。
 * 与上面的渲染分支必须同源：多算一处就会拓出一块盖着空气的深色底。
 */
export function hasSessionTopBadges(session: SessionItem, isSubagent: boolean): boolean {
  if (isSubagent) {
    return false;
  }
  if (isWorktreeActive(session.worktree_active)) {
    return true;
  }
  if (session.aux_flags?.ask_user_waiting) {
    return true;
  }
  return hasSessionStatusBadges(session.aux_flags);
}

/**
 * 选中底色向上拓展的高度。徽标排最多探出盒子上沿 6px（名字顶 = 盒顶 + 边框 1 + 内边距 7，
 * 徽标高 14），取 9px 留 1px 富余，正好把它整排包进底色里。
 * 拓展靠绝对定位实现 —— **不占流**，行高与相邻行的位置一个像素都不变。
 */
export const TOP_BADGE_LIFT_PX = 9;
