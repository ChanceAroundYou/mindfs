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
export function ActivityTypeTag({ activity, color }: { activity: TaskActivity; color?: string }) {
  const { t } = useI18n();
  const key = activityTypeTagKey(activity);
  if (!key) return null;
  return (
    <span
      data-testid="activity-type-tag"
      data-activity-type={activity.type}
      data-activity-purpose={activity.purpose || ""}
      // 颜色只覆盖文字，外形仍只有 activityMetaPillStyle 这一份；
      // 目前只有提醒消息按紧急程度传 color，其余分支保持次要文字色。
      style={color ? { ...activityMetaPillStyle, color } : activityMetaPillStyle}
    >
      {t(key)}
    </span>
  );
}

/**
 * 消息元信息胶囊的唯一外形：类型胶囊与排队徽标共用这一份。
 *
 * 胶囊是给用户看的，恒为满不透明；「次要」只由旁边的时间/工作流名/入口键的
 * opacity 表达。**不要给行容器挂 opacity** —— 父级不透明度子级无法覆盖，整枚胶囊
 * 会一起被压淡（2026-11-25 截图：Agent 页脚的「结果」比系统行的「提醒」浅一半）。
 */
export const activityMetaPillStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 999,
  padding: "1px 6px",
  fontSize: 10,
  fontWeight: 700,
  lineHeight: 1.5,
  color: "var(--text-secondary)",
};
