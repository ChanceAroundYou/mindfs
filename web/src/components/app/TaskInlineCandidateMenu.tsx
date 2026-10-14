import type { RefObject } from "react";
import type { CandidateItem } from "../../shared/candidateTypes";

export type TaskInlineCandidateMenuProps = {
  visible: boolean;
  isMobile: boolean;
  candidates: CandidateItem[];
  selectedIndex: number;
  itemRefs: RefObject<(HTMLButtonElement | null)[]>;
  onSelect: (candidate: CandidateItem) => void;
  onSelectedIndexChange: (index: number) => void;
};

export function TaskInlineCandidateMenu({
  visible,
  isMobile,
  candidates,
  selectedIndex,
  itemRefs,
  onSelect,
  onSelectedIndexChange,
}: TaskInlineCandidateMenuProps) {
  if (!visible || candidates.length === 0) return null;

  return (
    <div
      style={{
        position: "absolute",
        left: "12px",
        right: "12px",
        bottom: "calc(100% + 6px)",
        maxHeight: isMobile ? "min(42vh, 260px)" : "260px",
        overflowY: "auto",
        border: "1px solid var(--menu-border)",
        borderRadius: "8px",
        background: "var(--menu-bg)",
        boxShadow: "0 12px 28px rgba(15, 23, 42, 0.14)",
        zIndex: 2,
      }}
    >
      {candidates.map((candidate, index) => (
        <button
          key={`${candidate.type}:${candidate.name}`}
          ref={(node) => {
            itemRefs.current[index] = node;
          }}
          type="button"
          onMouseDown={(event) => {
            event.preventDefault();
            onSelect(candidate);
          }}
          style={{
            width: "100%",
            border: "none",
            borderTop: index === 0 ? "none" : "1px solid var(--menu-divider)",
            background: index === selectedIndex ? "var(--menu-active-bg)" : "transparent",
            color: "var(--text-primary)",
            display: "flex",
            flexDirection: "column",
            alignItems: "flex-start",
            gap: "2px",
            padding: "9px 10px",
            textAlign: "left",
            cursor: "pointer",
          }}
          onMouseEnter={() => onSelectedIndexChange(index)}
        >
          <span style={{ fontSize: "13px", fontWeight: 700 }}>
            {candidate.type === "file" ? `@${candidate.name}` : candidate.type === "prompt" ? `#${candidate.name}` : candidate.type === "slash_command" ? `/${candidate.name}` : candidate.name}
          </span>
          {candidate.description ? (
            <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{candidate.description}</span>
          ) : null}
        </button>
      ))}
    </div>
  );
}
