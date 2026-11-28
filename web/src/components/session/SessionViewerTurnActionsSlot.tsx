import React, { useMemo } from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { MessageKey, Locale } from "../../i18n/index";
import { formatSessionDuration } from "../../shared/sessionDuration";
import { SessionViewerTurnActions } from "./SessionViewerTurnActions";
import { useTurnCopyAction } from "./useTurnCopyAction";
import { findLatestCompletedTurnIndex, formatTime } from "./sessionViewerFormat";

/**
 * 底部尾行的左侧内容：最新一个**已完整结束**轮次的复制 / fork / 完成时间。
 *
 * 历史轮次的控件行仍在时间线各自的收尾位置；最新轮统一提到尾行，与右侧状态条同排，
 * 运行中/子代理运行时让位给等待提示（互斥优先级见 SessionViewer）。
 */
export type SessionViewerTurnActionsSlotProps = {
  timeline: TimelineItem[];
  timelineItemMeta: Array<{ userIndex: number; prevUserTs: string | undefined }>;
  locale: Locale;
  copiedMessageKeys: Record<string, true>;
  copyResetTimersRef: MutableRefObject<Record<string, number>>;
  setCopiedMessageKeys: Dispatch<SetStateAction<Record<string, true>>>;
  t: (key: MessageKey) => string;
  themeColor: string;
  userMetaButtonStyle: React.CSSProperties;
  onForkAgentMessage?: (seq: number) => void | Promise<void>;
};

export function SessionViewerTurnActionsSlot({
  timeline,
  timelineItemMeta,
  locale,
  copiedMessageKeys,
  copyResetTimersRef,
  setCopiedMessageKeys,
  t,
  themeColor,
  userMetaButtonStyle,
  onForkAgentMessage,
}: SessionViewerTurnActionsSlotProps) {
  const turnCopy = useTurnCopyAction({
    copiedMessageKeys,
    copyResetTimersRef,
    setCopiedMessageKeys,
    t,
  });
  const latestIndex = useMemo(
    () => findLatestCompletedTurnIndex(timeline),
    [timeline],
  );
  const item = latestIndex >= 0 ? timeline[latestIndex] : null;
  const meta = item?.turnMeta;
  if (!item || !meta?.completed) return null;

  const { copied, onCopy } = turnCopy(meta);
  const seq = Number(meta.seq || 0);
  const onFork =
    seq > 0 && onForkAgentMessage
      ? () => {
          void onForkAgentMessage(seq);
        }
      : undefined;

  return (
    <SessionViewerTurnActions
      time={formatTime(meta.timestamp, locale)}
      durationLabel={formatSessionDuration(
        timelineItemMeta[latestIndex]?.prevUserTs,
        meta.timestamp,
      )}
      copied={copied}
      t={t}
      themeColor={themeColor}
      userMetaButtonStyle={userMetaButtonStyle}
      marginTop="0"
      onCopy={onCopy}
      onFork={onFork}
    />
  );
}