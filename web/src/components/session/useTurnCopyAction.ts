import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { TurnMeta } from "../../hooks/sessionBlocks";
import type { MessageKey } from "../../i18n/index";
import { useReportError } from "../../hooks/useReportError";
import { copyText } from "../../platform/clipboard";

/**
 * 轮次复制按钮的唯一接线：复制正文 → 标记 turnKey → 1s 后复位。
 *
 * 每轮控件行（历史轮，在时间线内）与底部尾行（最新轮，与状态条同行）都可能渲染
 * 同一个按钮，复制状态必须共用同一套 key/定时器，不能各写一份。
 */
export function useTurnCopyAction({
  copiedMessageKeys,
  copyResetTimersRef,
  setCopiedMessageKeys,
  t,
}: {
  copiedMessageKeys: Record<string, true>;
  copyResetTimersRef: MutableRefObject<Record<string, number>>;
  setCopiedMessageKeys: Dispatch<SetStateAction<Record<string, true>>>;
  t: (key: MessageKey) => string;
}) {
  const reportError = useReportError();
  return (meta: TurnMeta) => {
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
    return { copied, onCopy };
  };
}