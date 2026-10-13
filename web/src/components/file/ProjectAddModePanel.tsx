import type React from "react";
import { useI18n } from "../../i18n/index";
import { projectAddPopoverStyle } from "./projectAddStyles";

type ProjectAddModePanelProps = {
  onSelectBlankLocation: () => void;
  onSelectLocal: () => void;
  onSelectGitHubLocation: () => void;
};

function ModeItem({
  label,
  icon,
  iconColor,
  onClick,
}: {
  label: string;
  icon: React.ReactNode;
  iconColor?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "8px",
        width: "100%",
        border: "1px solid transparent",
        background: "transparent",
        color: "var(--text-primary)",
        borderRadius: "8px",
        padding: "8px 10px",
        cursor: "pointer",
        textAlign: "left",
        fontSize: "12px",
      }}
    >
      <span
        style={{
          width: "18px",
          height: "18px",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          color: iconColor || "var(--text-secondary)",
          flexShrink: 0,
        }}
      >
        {icon}
      </span>
      <span>{label}</span>
    </button>
  );
}

export function ProjectAddModePanel({
  onSelectBlankLocation,
  onSelectLocal,
  onSelectGitHubLocation,
}: ProjectAddModePanelProps) {
  const { t } = useI18n();
  return (
    <div style={projectAddPopoverStyle}>
      <ModeItem
        label={t("projectAdd.blankProject")}
        onClick={onSelectBlankLocation}
        iconColor="var(--accent-color)"
        icon={
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        }
      />
      <ModeItem
        label={t("projectAdd.localDirectory")}
        onClick={onSelectLocal}
        iconColor="var(--accent-color)"
        icon={
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M3 6h5l2 2h11" />
            <path d="M3 6v11a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V8H10L8 6H5a2 2 0 0 0-2 2" />
          </svg>
        }
      />
      <ModeItem
        label={t("projectAdd.githubImport")}
        onClick={onSelectGitHubLocation}
        icon={
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 2A10 10 0 0 0 2 12c0 4.42 2.87 8.17 6.84 9.5c.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34c-.46-1.16-1.11-1.47-1.11-1.47c-.91-.62.07-.6.07-.6c1 .07 1.53 1.03 1.53 1.03c.87 1.52 2.34 1.07 2.91.83c.09-.65.35-1.09.63-1.34c-2.22-.25-4.55-1.11-4.55-4.92c0-1.11.38-2 1.03-2.71c-.1-.25-.45-1.29.1-2.64c0 0 .84-.27 2.75 1.02c.79-.22 1.65-.33 2.5-.33s1.71.11 2.5.33c1.91-1.29 2.75-1.02 2.75-1.02c.55 1.35.2 2.39.1 2.64c.65.71 1.03 1.6 1.03 2.71c0 3.82-2.34 4.66-4.57 4.91c.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0 0 12 2" />
          </svg>
        }
      />
    </div>
  );
}
