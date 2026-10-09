import { useCallback } from "react";

import type { I18nContextValue } from "../i18n";

export type UseAppFormatDirectoryLoadErrorParams = {
  t: I18nContextValue["t"];
};

/**
 * 把目录读取失败的原始报错翻译成人话 —— 只认两类：读不到（权限），和读到了但是空的。
 *
 * 后端给的 message 在不同平台/挂载方式下措辞不统一（Access is denied / permission denied /
 * operation not permitted / 拒绝访问 / 权限），所以按子串匹配而不是等值比较。
 * 匹配不上就原样透出，别把未知原因吃掉 —— 用户至少能看到原文。
 */
export function useAppFormatDirectoryLoadError(
  params: UseAppFormatDirectoryLoadErrorParams,
) {
  const { t } = params;

  const formatDirectoryLoadError = useCallback(
    (message?: string | null) => {
      const text = String(message || "").trim();
      if (!text) {
        return t("directory.readFailed");
      }
      const lower = text.toLowerCase();
      if (
        lower.includes("access is denied") ||
        lower.includes("permission denied") ||
        lower.includes("operation not permitted") ||
        lower.includes("拒绝访问") ||
        lower.includes("权限")
      ) {
        return t("directory.permissionDenied");
      }
      return text;
    },
    [t],
  );

  return { formatDirectoryLoadError };
}