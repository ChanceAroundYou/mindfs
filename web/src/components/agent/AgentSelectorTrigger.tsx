import { AgentIcon } from "./AgentIcon";

export type AgentSelectorTriggerProps = {
  agent: string;
  compact: boolean;
  showChevron: boolean;
  warnUnavailable?: boolean;
  buttonTitle?: string;
  onClick: () => void;
};

export function AgentSelectorTrigger({
  agent,
  compact,
  showChevron,
  warnUnavailable,
  buttonTitle,
  onClick,
}: AgentSelectorTriggerProps) {
  return (
    <button
      type="button"
      onMouseDown={(event) => event.preventDefault()}
      onClick={onClick}
      title={buttonTitle}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "4px",
        padding: compact ? "4px 4px" : "6px 8px",
        borderRadius: "12px",
        border: "none",
        background: "transparent",
        cursor: "pointer",
        fontSize: "16px",
        transition: "background 0.2s",
        outline: "none",
        position: "relative",
      }}
      onMouseEnter={(event) => {
        if (compact) event.currentTarget.style.background = "rgba(0,0,0,0.05)";
      }}
      onMouseLeave={(event) => {
        if (compact) event.currentTarget.style.background = "transparent";
      }}
    >
      <AgentIcon agentName={agent} style={{ width: "16px", height: "16px" }} />
      {showChevron ? (
        <svg
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          style={{ color: "var(--text-secondary)" }}
        >
          <path d="m6 9 6 6 6-6" />
        </svg>
      ) : null}
      {warnUnavailable ? (
        <span
          style={{
            position: "absolute",
            top: "3px",
            right: "3px",
            minWidth: "11px",
            height: "11px",
            padding: "0 2px",
            borderRadius: "50%",
            background: "#d97706",
            color: "#fff",
            fontSize: "9px",
            lineHeight: "11px",
            fontWeight: 700,
            textAlign: "center",
            boxShadow: "0 0 0 1px rgba(255,255,255,0.95)",
          }}
        >
          !
        </span>
      ) : null}
    </button>
  );
}
