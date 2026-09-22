import React from "react";
import { useI18n } from "../i18n";

export type MainViewMode = "workspace" | "board" | "files" | "chat";

export type MainViewSwitcherProps = {
  value: MainViewMode;
  onChange: (mode: MainViewMode) => void;
  accentColor?: string;
};

// 左栏底部的四态切换器：主区内容的唯一手动入口（见 docs/main-view-switching-design.md）。
function Glyph({ mode }: { mode: MainViewMode }) {
  const common = { width: 15, height: 15, viewBox: "0 0 24 24", fill: "currentColor", "aria-hidden": true } as const;
  if (mode === "workspace") {
    return (
      <svg {...common}>
        <path d="M4 4h7v7H4zm9 0h7v7h-7zM4 13h7v7H4zm9 0h7v7h-7z" />
      </svg>
    );
  }
  if (mode === "board") {
    return (
      <svg {...common}>
        <path d="M4 4h4.5v16H4zm5.75 0h4.5v10h-4.5zm5.75 0H20v13h-4.5z" />
      </svg>
    );
  }
  if (mode === "files") {
    return (
      <svg {...common}>
        <path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
      </svg>
    );
  }
  return (
    <svg {...common}>
      <path d="M4 4h16a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H9l-5 4V5a1 1 0 0 1 1-1z" />
    </svg>
  );
}

const MODES: MainViewMode[] = ["workspace", "board", "files", "chat"];
const LABEL_KEYS = {
  workspace: "view.workspace",
  board: "view.board",
  files: "view.files",
  chat: "view.chat",
} as const;

export function MainViewSwitcher({ value, onChange, accentColor }: MainViewSwitcherProps) {
  const { t } = useI18n();
  const accent = accentColor || "var(--accent-color, #2563eb)";
  return (
    <div
      role="tablist"
      aria-label={t("view.switcherLabel")}
      data-onboarding="main-view-switcher"
      style={{
        flexShrink: 0,
        display: "grid",
        gridTemplateColumns: "repeat(4, minmax(0, 1fr))",
        gap: 2,
        padding: 6,
        borderTop: "1px solid var(--border-color)",
        background: "var(--mindfs-panel-bg, transparent)",
      }}
    >
      {MODES.map((mode) => {
        const active = value === mode;
        return (
          <button
            key={mode}
            type="button"
            role="tab"
            aria-selected={active}
            title={t(LABEL_KEYS[mode])}
            onClick={() => onChange(mode)}
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              gap: 3,
              padding: "6px 2px",
              border: "none",
              borderRadius: 8,
              cursor: "pointer",
              background: active ? "var(--selection-bg, rgba(37, 99, 235, 0.12))" : "transparent",
              color: active ? accent : "var(--text-secondary)",
              fontSize: 10,
              fontWeight: 700,
              lineHeight: 1.1,
            }}
          >
            <Glyph mode={mode} />
            <span style={{ whiteSpace: "nowrap" }}>{t(LABEL_KEYS[mode])}</span>
          </button>
        );
      })}
    </div>
  );
}
