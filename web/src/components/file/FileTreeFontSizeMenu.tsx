import React from "react";
export function FileTreeFontSizeMenu({ open, rows, prefs, min, max, onChange, changeFontSize, buttonStyle, t }: any) {
  if (!open) return null;
  return <div style={{ padding: "2px 6px 6px", display: "flex", flexDirection: "column", gap: "3px" }}>
    {rows.map(({ region, labelKey }: any) => {
      const label = t(labelKey);
      const scale = prefs[region];
      return <div key={region} style={{ minHeight: "30px", padding: "0 4px", display: "flex", alignItems: "center", gap: "6px", color: "var(--text-primary)", fontSize: "12px" }}>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }} title={label}>{label}</span>
        <div style={{ flexShrink: 0, display: "inline-flex", alignItems: "center", border: "1px solid var(--border-color)", borderRadius: "7px", overflow: "hidden" }}>
          <button type="button" className="mindfs-font-size-adjust" disabled={scale <= min} aria-label={t("fileTree.fontSizeDecrease", { region: label })} onClick={() => onChange?.(changeFontSize(prefs, region, -1))} style={buttonStyle}>−</button>
          <span style={{ width: "42px", textAlign: "center", color: "var(--text-secondary)", fontSize: "11px", fontVariantNumeric: "tabular-nums" }}>{Math.round(scale * 100)}%</span>
          <button type="button" className="mindfs-font-size-adjust" disabled={scale >= max} aria-label={t("fileTree.fontSizeIncrease", { region: label })} onClick={() => onChange?.(changeFontSize(prefs, region, 1))} style={buttonStyle}>+</button>
        </div>
      </div>;
    })}
  </div>;
}
