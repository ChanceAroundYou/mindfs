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
 * 高度上限：这排最多 14px（worktree 标签与 aux 徽标同高）。名字顶在行盒顶下方
 * 10px（行 padding 2 + 边框 1 + 盒内边距 7），所以徽标排里探出行盒上沿的是 4px
 * —— 见 TOP_BADGE_OVERHANG_PX。
 */
export function SessionCardTopBadges({
  session,
  isSubagent,
  nodeColor,
  unread = false,
}: {
  session: SessionItem;
  isSubagent: boolean;
  nodeColor?: string;
  unread?: boolean;
}) {
  const { t } = useI18n();
  if (isSubagent || !hasSessionTopBadges(session, isSubagent, unread)) {
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
      {/* 「完成未读」排最左：它是这一行最新的信息，其余徽标描述的是持久状态。 */}
      {unread ? (
        <span
          aria-label={t("sessionList.unreadCompleted")}
          title={t("sessionList.unreadCompleted")}
          style={{
            ...taskAuxBadgeStyle(false),
            // 与项目面包屑徽标同一个底色变量（AGENTS.md §11）：亮/暗两套主题各自定义，
            // 这里的图标色是节点色，两者叠起来才读得出「这是本项目的新结果」。
            background: "var(--node-badge-bg)",
            color: nodeColor || "var(--accent-color)",
            pointerEvents: "auto",
          }}
        >
          <span style={taskAuxIconScaleStyle()}>
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="3"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <polyline points="20 6 9 17 4 12" />
            </svg>
          </span>
        </span>
      ) : null}
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
export function hasSessionTopBadges(session: SessionItem, isSubagent: boolean, unread = false): boolean {
  if (isSubagent) {
    return false;
  }
  if (unread) {
    return true;
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
 * 徽标排探出**行盒**上沿的高度。
 *
 * 行盒顶 → 名字顶是 10px（行 padding 2 + 底盒边框 1 + 底盒内边距 7），徽标排高 14px
 * （worktree 标签与 aux 徽标同高），所以露在行盒外面的只有 4px。
 */
export const TOP_BADGE_OVERHANG_PX = 4;

/**
 * 选中底色向上拓展的高度 = 行外那 2px 留白 + 徽标探出的 4px。
 *
 * 这个数两边都卡着，多一个像素都不行（2026-11-28 用户报的「会话列表相互覆盖」就是
 * 这里越界）：
 * - 小于 6：底色包不住整排徽标（2026-10-16 用户要求包住）；
 * - 大于「行距 + 2」：底色顶越过行距，压进上一行的盒子里。上一行若是任务分组头，
 *   它的悬停底色铺满整行，两层半透明底一叠就是一道更亮的横带 —— 看着就是两张卡
 *   相互覆盖。此前 lift 9 + 行距 2 正好越界 5px，手机上截图一眼可见。
 * 行距必须跟着给到 SESSION_ROW_GAP_PX，单改一个数会把这条约束重新踩坏。
 *
 * 拓展靠负外边距 + 等量上内边距实现 —— 占的是行外的留白，行高与相邻行的位置
 * 一个像素都不变。
 */
export const TOP_BADGE_LIFT_PX = TOP_BADGE_OVERHANG_PX + 2;

/**
 * 列表里会话行之间的 gap。**这不是审美值，是拓位的落点**：
 * 拓位顶 = 行盒顶 - (lift - 行 padding 2) = 行盒顶 - 4px，正好落在徽标排顶端；
 * 行距 5px 时它离上一行盒底还有 1px（亚像素留量，免得两层底色共用一个边界像素）。
 */
export const SESSION_ROW_GAP_PX = TOP_BADGE_OVERHANG_PX + 1;
