import React, { useMemo } from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { MessageKey, Locale } from "../../i18n/index";
import { useReportError } from "../../hooks/useReportError";
import { copyText } from "../../platform/clipboard";
import { formatSessionDuration } from "../../shared/sessionDuration";
import { SessionViewerTurnActions } from "./SessionViewerTurnActions";
import { findLatestCompletedTurnIndex, formatTime } from "./sessionViewerFormat";

/**
 * 底部控件槽：只展示**最新一个已完整结束**轮次的复制 / fork / 完成时间。
 *
 * 不再逐轮挂在时间线末尾 —— 位置统一由 `SessionViewerBottomBar` 管理，运行中让位给
 * 等待提示（互斥的优先级见 SessionViewer）。复制反馈仍按 turnKey（`s<seq>`）记，
 * 同轮分组卡/缓存重建不会丢。
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
  const reportError = useReportError();
  const latestIndex = useMemo(
    () => findLatestCompletedTurnIndex(timeline),
    [timeline],
  );
  const item = latestIndex >= 0 ? timeline[latestIndex] : null;
  const meta = item?.turnMeta;
  if (!item || !meta?.completed) return null;

  const footerKey = meta.turnKey;
  const copied = !!copiedMessageKeys[footerKey];
  const onCopy = () => {
    if (!meta.content) {
      reportError("clipboard.write_failed", t("session.emptyMessageCannotCopy"));
      return;
    }
    void copyText(meta.content)
      .then(() => {
        setCopiedMessageKeys((prev) => ({ ...prev, [footerKey]: true }));
        if (copyResetTimersRef.current[footerKey]) {
          window.clearTimeout(copyResetTimersRef.current[footerKey]);
        }
        copyResetTimersRef.current[footerKey] = window.setTimeout(() => {
          setCopiedMessageKeys((prev) => {
            const next = { ...prev };
            delete next[footerKey];
            return next;
          });
          delete copyResetTimersRef.current[footerKey];
        }, 1000);
      })
      .catch((err: unknown) => {
        reportError(
          "clipboard.write_failed",
          String((err as Error)?.message || t("session.copyFailed")),
        );
      });
  };
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