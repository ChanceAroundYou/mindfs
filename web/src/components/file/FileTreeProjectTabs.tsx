import React from "react";

export function FileTreeProjectTabs({ projectTreeTab, setProjectTreeTab, t, accentColor, hexToRgbaApp }: any) {
  return (
<div
  role="tablist"
  data-onboarding="project-tabs"
  aria-label={t("fileTree.projectTabs")}
  style={{
    display: "flex",
    alignItems: "center",
    gap: 0,
    padding: "2px",
    borderRadius: "8px",
    border: "1px solid rgba(100, 116, 139, 0.36)",
    background: "rgba(148, 163, 184, 0.10)",
    minWidth: 0,
    width: "100%",
  }}
>
  {([
    ["files", t("fileTree.files")],
    ["git", "git"],
    ["worktrees", t("fileTree.worktrees")],
    ["related", t("fileTree.relatedFiles")],
  ] as const).map(([value, label], index) => {
    const active = projectTreeTab === value;
    const flexGrow = value === "related" ? 1.45 : value === "worktrees" ? 1.15 : 0.85;
    const tabAccent = accentColor;
    return (
      <React.Fragment key={value}>
        {index > 0 ? (
          <span
            aria-hidden="true"
            style={{
              width: "1px",
              height: "16px",
              background: "rgba(100, 116, 139, 0.32)",
              margin: "0 1px",
              flexShrink: 0,
            }}
          />
        ) : null}
        <button
          type="button"
          role="tab"
          aria-selected={active}
          onClick={() => setProjectTreeTab(value)}
          style={{
            border: "none",
            borderRadius: "6px",
            background: active ? tabAccent : "transparent",
            color: active ? "#fff" : "var(--text-secondary)",
            padding: "3px 5px",
            fontSize: "11px",
            fontWeight: 700,
            lineHeight: "14px",
            cursor: "pointer",
            whiteSpace: "nowrap",
            minWidth: 0,
            flex: `${flexGrow} 1 auto`,
            boxShadow: active ? `0 1px 3px ${hexToRgbaApp(tabAccent, 0.28)}` : "none",
          }}
        >
          {label}
        </button>
      </React.Fragment>
    );
  })}
</div>
  );
}
