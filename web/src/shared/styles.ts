import type React from "react";

/** 透明图标按钮（28×28，8px 圆角），withGap 控制水平 padding 与文字间距。 */
export function iconButtonStyle(withGap: boolean): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    gap: withGap ? "2px" : 0,
    height: "28px",
    minWidth: "28px",
    borderRadius: "8px",
    cursor: "pointer",
    padding: withGap ? "0 6px" : 0,
  };
}

/** 全宽菜单项样式（12px 字号，8px 圆角）。 */
export const menuItemStyle: React.CSSProperties = {
  width: "100%",
  border: "none",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: "8px",
  padding: "8px 10px",
  display: "flex",
  alignItems: "center",
  gap: "8px",
  textAlign: "left",
  cursor: "pointer",
  fontSize: "12px",
};

/** 行内小按钮（11px 字号，4px 圆角）。 */
export const inlineActionStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  color: "var(--text-secondary)",
  cursor: "pointer",
  padding: "2px 4px",
  borderRadius: "4px",
  fontSize: "11px",
};

/** 空/加载态提示文字。 */
export const emptyStyle: React.CSSProperties = {
  fontSize: "12px",
  color: "var(--text-secondary)",
  padding: "12px 8px",
};

/** 错误态提示文字，基于 emptyStyle 换成 danger 色。 */
export const errorStyle: React.CSSProperties = {
  ...emptyStyle,
  color: "var(--danger-color, #b42318)",
  whiteSpace: "pre-wrap",
};
