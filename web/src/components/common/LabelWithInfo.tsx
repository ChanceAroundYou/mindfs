import React from "react";
import { useI18n } from "../../i18n";

/**
 * 标签 + 行内「i」说明按钮。说明展开后就地显示，不弹层。
 *
 * 新建任务面板等表单共用。
 */
export function LabelWithInfo({ label, info, helpKey, openHelpKey, setOpenHelpKey }: {
  label: string;
  info: string;
  helpKey: string;
  openHelpKey: string;
  setOpenHelpKey: (key: string) => void;
}) {
  const { t } = useI18n();
  const open = openHelpKey === helpKey;
  return (
    <span style={{ ...labelStyle, position: "relative", display: "inline-flex", alignItems: "center", gap: "5px", width: "fit-content" }}>
      {label}
      <button
        type="button"
        aria-label={t("task.fieldInfo", { label })}
        onMouseDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setOpenHelpKey(open ? "" : helpKey);
        }}
        style={{
          width: "15px",
          height: "15px",
          borderRadius: "999px",
          border: "1px solid var(--border-color)",
          background: open ? "var(--selection-bg)" : "transparent",
          color: open ? "var(--accent-color)" : "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 0,
          fontSize: "10px",
          fontWeight: 800,
          cursor: "pointer",
          lineHeight: 1,
        }}
      >
        i
      </button>
      {open ? (
        <span style={{ color: "var(--text-secondary)", fontSize: "11px", fontWeight: 500, lineHeight: 1.35 }}>
          {info}
        </span>
      ) : null}
    </span>
  );
}

const labelStyle: React.CSSProperties = { fontSize: "11px", color: "var(--text-secondary)", fontWeight: 700 };
