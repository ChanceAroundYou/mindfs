import type { RefObject } from "react";
import { useI18n } from "../../i18n/index";
import type { GitBranchItem } from "../../shared/gitTypes";

export type GitBranchSelectorProps = {
  branch: string;
  compact: boolean;
  enabled: boolean;
  menuRef: RefObject<HTMLDivElement | null>;
  isOpen: boolean;
  branches: GitBranchItem[];
  loading: boolean;
  switchingBranch: string;
  onToggle: () => void;
  onSwitch: (branch: string) => Promise<void>;
};

export function GitBranchSelector({
  branch,
  compact,
  enabled,
  menuRef,
  isOpen,
  branches,
  loading,
  switchingBranch,
  onToggle,
  onSwitch,
}: GitBranchSelectorProps) {
  const { t } = useI18n();
  return (
    <div
      ref={menuRef}
      className={compact ? "mindfs-git-branch-wrap mindfs-git-branch-wrap-compact" : "mindfs-git-branch-wrap"}
      data-branch-tooltip={isOpen ? undefined : branch}
      style={{ position: "relative", minWidth: 0, flex: "1 1 auto", maxWidth: "100%" }}
    >
      <button
        type="button"
        className="mindfs-git-branch-button"
        aria-label={`当前分支：${branch}`}
        onClick={onToggle}
        disabled={!enabled}
        style={{
          border: "none",
          background: isOpen ? "rgba(15, 23, 42, 0.06)" : "transparent",
          color: "var(--text-primary)",
          borderRadius: "7px",
          padding: "3px 6px",
          display: "inline-flex",
          alignItems: "center",
          gap: "4px",
          width: "100%",
          cursor: enabled ? "pointer" : "default",
        }}
      >
        <span className="mindfs-git-branch-label" style={{ fontSize: "12px", fontWeight: 600 }}>
          {branch}
        </span>
        {enabled ? (
          <svg
            width="12"
            height="12"
            viewBox="0 0 20 20"
            fill="currentColor"
            aria-hidden="true"
            style={{
              color: "var(--text-secondary)",
              flexShrink: 0,
              transform: isOpen ? "rotate(180deg)" : "rotate(0deg)",
              transition: "transform 0.15s",
            }}
          >
            <path
              fillRule="evenodd"
              d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.17l3.71-3.94a.75.75 0 1 1 1.08 1.04l-4.25 4.5a.75.75 0 0 1-1.08 0l-4.25-4.5a.75.75 0 0 1 .02-1.06"
              clipRule="evenodd"
            />
          </svg>
        ) : null}
      </button>
      {enabled && isOpen ? (
        <div
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            left: 0,
            minWidth: "180px",
            maxWidth: "260px",
            maxHeight: "260px",
            overflow: "auto",
            padding: "6px",
            borderRadius: "10px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 25,
          }}
        >
          {loading ? (
            <div style={{ padding: "8px 10px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("git.loading")}</div>
          ) : branches.length === 0 ? (
            <div style={{ padding: "8px 10px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("git.noBranches")}</div>
          ) : branches.map((item) => {
            const active = item.name === branch;
            const busy = switchingBranch === item.name;
            return (
              <button
                key={item.name}
                type="button"
                title={item.name}
                disabled={active || !!switchingBranch}
                onClick={() => void onSwitch(item.name)}
                style={{
                  width: "100%",
                  border: "none",
                  background: active ? "var(--selection-bg)" : "transparent",
                  color: active ? "var(--accent-color)" : "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: "12px",
                  textAlign: "left",
                  cursor: active || switchingBranch ? "default" : "pointer",
                  fontSize: "12px",
                  opacity: switchingBranch && !busy ? 0.58 : 1,
                }}
              >
                <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {item.name}
                </span>
                <span style={{ fontSize: "11px", color: "var(--text-secondary)", flexShrink: 0 }}>
                  {busy ? "..." : active ? "✓" : ""}
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
