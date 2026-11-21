import React from "react";
import { useI18n } from "../../i18n/index";
import type { TaskActivity } from "../../shared/taskComments";
import { activityTypeTagKey } from "../../shared/activityModel";

/**
 * 消息类型胶囊：这条消息是留言、回复、结果、进度、总结……还是系统事件。
 *
 * 这是**类型**，不是状态：任务级状态只出现在详情面板标题栏（2026-11-21 已确认
 * 不再逐条挂状态）。文案 key 由 activityModel.activityTypeTagKey 从后端枚举推导，
 * 位置固定在页脚时间前面，和排队徽标、打开会话等元信息同一档字号。
 */
export function ActivityTypeTag({ activity }: { activity: TaskActivity }) {
  const { t } = useI18n();
  const key = activityTypeTagKey(activity);
  if (!key) return null;
  return (
    <span
      data-testid="activity-type-tag"
      data-activity-type={activity.type}
      data-activity-purpose={activity.purpose || ""}
      style={typeTagStyle}
    >
      {t(key)}
    </span>
  );
}

const typeTagStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 999,
  padding: "1px 6px",
  fontSize: 9,
  fontWeight: 700,
  lineHeight: 1.5,
  color: "var(--text-secondary)",
  // Agent 页脚整体 opacity 0.5；类型胶囊是给用户看的，单独恢复不透明度。
  opacity: 1,
};
