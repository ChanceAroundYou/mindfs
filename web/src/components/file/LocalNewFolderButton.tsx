import React from "react";
import { useI18n } from "../../i18n/index";

/**
 * 路径面包屑行尾的「新建文件夹」按钮。
 *
 * 从 ProjectAddPopover 抽出来的唯一理由是该文件撞了 500 行硬线；职责就这一颗按钮，
 * 禁用态只取决于「当前有没有目录可落在其中 / 是否正在建 / 正在加载」。
 */
export function LocalNewFolderButton({
  path,
  loading,
  creating,
  onCreateFolder,
}: {
  path: string;
  loading: boolean;
  creating: boolean;
  onCreateFolder: () => void;
}) {
  const { t } = useI18n();
  const disabled = !path || loading || creating;
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onCreateFolder}
      aria-label={t("projectAdd.newFolder")}
      title={t("projectAdd.newFolder")}
      style={{
        marginLeft: "auto",
        border: "none",
        background: "transparent",
        padding: 0,
        color: "var(--text-secondary)",
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.35 : 1,
        display: "inline-flex",
        alignItems: "center",
        flexShrink: 0,
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M12 5v14M5 12h14" />
      </svg>
    </button>
  );
}
