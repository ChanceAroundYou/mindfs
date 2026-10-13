import React from "react";
import { type GitStatusItem, type GitStatusPayload } from "../../services/git";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import {
  renderStatusColor,
  renderLineStat,
  renderStatusLabel,
  compactPath,
} from "./gitStatusHelpers";
import {
  GitPullIcon,
  GitPushIcon,
  GitCommitIcon,
  CommitSubmitIcon,
  OpenFileIcon,
  UndoIcon,
  StageIcon,
  UnstageIcon,
  GitIconButton,
} from "./gitStatusIcons";
import { useGitStatusPanel } from "./useGitStatusPanel";
import { GitBranchSelector } from "./GitBranchSelector";

type GitStatusPanelProps = {
  rootId?: string;
  status: GitStatusPayload | null;
  loading?: boolean;
  isFiltered?: boolean;
  expanded?: boolean;
  compact?: boolean;
  showHeader?: boolean;
  showHeaderActions?: boolean;
  showExpandedToggle?: boolean;
  enableBranchMenu?: boolean;
  onSelectItem?: (item: GitStatusItem) => void;
  onOpenItem?: (item: GitStatusItem) => void;
  onDiscardItem?: (item: GitStatusItem) => void | Promise<void>;
  onStageItem?: (item: GitStatusItem) => void | Promise<void>;
  onPull?: () => void | Promise<void>;
  onPush?: () => void | Promise<void>;
  onCommit?: (message: string) => void | Promise<void>;
  onSwitchBranch?: (branch: string) => void | Promise<void>;
  onExpandedChange?: (expanded: boolean) => void;
};

export function GitStatusPanel({
  rootId,
  status,
  loading = false,
  isFiltered = false,
  expanded = true,
  compact = false,
  showHeader = true,
  showHeaderActions = true,
  showExpandedToggle = true,
  enableBranchMenu = true,
  onSelectItem,
  onOpenItem,
  onDiscardItem,
  onStageItem,
  onPull,
  onPush,
  onCommit,
  onSwitchBranch,
  onExpandedChange,
}: GitStatusPanelProps) {
  const { t } = useI18n();
  const {
    branchMenuRef,
    branchMenuOpen,
    branches,
    branchesLoading,
    switchingBranch,
    commitOpen,
    commitMessage,
    actionBusy,
    selectedActionPath,
    hoveredActionPath,
    setBranchMenuOpen,
    setSwitchingBranch,
    setCommitOpen,
    setCommitMessage,
    setActionBusy,
    setSelectedActionPath,
    setHoveredActionPath,
    runPanelAction,
    submitCommit,
  } = useGitStatusPanel({ rootId, onCommit });

  if (!loading && (!status || status.available !== true)) {
    return null;
  }

  const items = status?.items || [];
  const hasStatusItems = items.length > 0;
  const headerMarginBottom = commitOpen
    ? "6px"
    : expanded && (loading || hasStatusItems)
      ? "10px"
      : 0;

  return (
    <section style={{ padding: 0, flexShrink: 0, minWidth: 0 }}>
      {showHeader ? (
        <div
          className="mindfs-git-status-header"
          style={{
            display: "grid",
            gridTemplateColumns: "minmax(0, 1fr) auto",
            alignItems: "center",
            gap: compact ? "6px" : "12px",
            marginBottom: headerMarginBottom,
            padding: compact ? "0 4px 0 0" : "0 10px",
            minWidth: 0,
            width: "100%",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: compact ? "6px" : "8px", minWidth: 0, overflow: "visible" }}>
            <span
              title={t("git.changes")}
              aria-label={t("git.changes")}
              style={{
                width: "18px",
                height: "18px",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                color: "var(--text-primary)",
                flexShrink: 0,
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path
                  fill="currentColor"
                  d="M7 5a2 2 0 1 1 3.763.945h.58a4 4 0 0 1 4 4v1.28a2 2 0 0 1-1.02 3.72a2 2 0 0 1-.98-3.745V9.945a2 2 0 0 0-2-2H10v9.323A2 2 0 0 1 9 21a2 2 0 0 1-1-3.732V6.732A2 2 0 0 1 7 5"
                />
              </svg>
            </span>
            {status?.branch ? (
              <GitBranchSelector
                branch={status.branch}
                compact={compact}
                enabled={!!(enableBranchMenu && rootId && onSwitchBranch)}
                menuRef={branchMenuRef}
                isOpen={branchMenuOpen}
                branches={branches}
                loading={branchesLoading}
                switchingBranch={switchingBranch}
                onToggle={() => {
                  if (enableBranchMenu && rootId && onSwitchBranch) {
                    setBranchMenuOpen((open) => !open);
                  }
                }}
                onSwitch={async (branchName) => {
                  setSwitchingBranch(branchName);
                  try {
                    await onSwitchBranch?.(branchName);
                    setBranchMenuOpen(false);
                  } finally {
                    setSwitchingBranch("");
                  }
                }}
              />
            ) : null}
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "1px", flex: "0 0 auto", minWidth: "fit-content" }}>
            {showHeaderActions ? (
              <>
                <GitIconButton
                  title={actionBusy === "pull" ? t("git.pulling") : "Git pull"}
                  disabled={!onPull || !!actionBusy}
                  onClick={() => runPanelAction("pull", onPull)}
                >
                  <GitPullIcon />
                </GitIconButton>
                <GitIconButton
                  title={actionBusy === "push" ? t("git.pushing") : "Git push"}
                  disabled={!onPush || !!actionBusy}
                  onClick={() => runPanelAction("push", onPush)}
                >
                  <GitPushIcon />
                </GitIconButton>
                <GitIconButton
                  title="Git commit"
                  disabled={!onCommit || !!actionBusy}
                  onClick={() => setCommitOpen((open) => !open)}
                >
                  <GitCommitIcon />
                </GitIconButton>
              </>
            ) : null}
            <div style={{ fontSize: "11px", fontWeight: 700, color: "var(--text-secondary)" }}>
              {loading ? "..." : items.length}
            </div>
            {showExpandedToggle ? (
              <button
                type="button"
                aria-label={expanded ? t("git.collapseChanges") : t("git.expandChanges")}
                title={expanded ? t("git.collapse") : t("git.expand")}
                onClick={() => onExpandedChange?.(!expanded)}
                style={{
                  width: "20px",
                  height: "20px",
                  border: "none",
                  borderRadius: "5px",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: "pointer",
                  padding: 0,
                }}
              >
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 20 20"
                  fill="currentColor"
                  aria-hidden="true"
                  style={{
                    transform: expanded ? "rotate(180deg)" : "rotate(0deg)",
                    transition: "transform 0.15s",
                  }}
                >
                  <path
                    fillRule="evenodd"
                    d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.17l3.71-3.94a.75.75 0 1 1 1.08 1.04l-4.25 4.5a.75.75 0 0 1-1.08 0l-4.25-4.5a.75.75 0 0 1 .02-1.06"
                    clipRule="evenodd"
                  />
                </svg>
              </button>
            ) : null}
          </div>
        </div>
      ) : null}

      {showHeaderActions && commitOpen ? (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "6px",
            marginBottom: "10px",
            padding: compact ? "0 4px 0 0" : "0 10px",
          }}
        >
          <input
            value={commitMessage}
            disabled={actionBusy === "commit"}
            autoFocus
            placeholder={t("git.commitMessagePlaceholder")}
            onChange={(event) => setCommitMessage(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                if (event.nativeEvent.isComposing) {
                  return;
                }
                event.preventDefault();
                void submitCommit();
              } else if (event.key === "Escape") {
                event.preventDefault();
                setCommitOpen(false);
              }
            }}
            style={{
              flex: 1,
              minWidth: 0,
              height: "26px",
              border: "1px solid var(--border-color)",
              borderRadius: "7px",
              background: "transparent",
              color: "var(--text-primary)",
              padding: "0 8px",
              fontSize: "12px",
              outline: "none",
            }}
          />
          <button
            type="button"
            title={actionBusy === "commit" ? t("git.committing") : t("git.commit")}
            aria-label={actionBusy === "commit" ? t("git.committing") : t("git.commit")}
            disabled={!commitMessage.trim() || actionBusy === "commit"}
            onClick={() => void submitCommit()}
            style={{
              width: "26px",
              height: "26px",
              border: "none",
              borderRadius: "7px",
              background: "var(--accent-color)",
              color: "#fff",
              padding: 0,
              fontSize: "15px",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              flexShrink: 0,
              cursor: !commitMessage.trim() || actionBusy === "commit" ? "not-allowed" : "pointer",
              opacity: !commitMessage.trim() || actionBusy === "commit" ? 0.55 : 1,
            }}
          >
            <CommitSubmitIcon />
          </button>
        </div>
      ) : null}

      {!expanded ? null : loading ? (
        <div style={{ fontSize: "12px", color: "var(--text-secondary)", padding: "6px 10px" }}>{t("git.loadingChanges")}</div>
      ) : !hasStatusItems ? null : (
        <div style={{ display: "flex", flexDirection: "column", gap: "6px", paddingLeft: compact ? 0 : "14px", minWidth: 0 }}>
          {items.map((item) => {
            const actionKey = `${item.status}:${item.path}`;
            const showActions = selectedActionPath === actionKey || hoveredActionPath === actionKey;
            return (
              <div
                key={actionKey}
                role="button"
                tabIndex={item.is_dir === true ? -1 : 0}
                onClick={() => {
                  setSelectedActionPath(actionKey);
                  if (item.is_dir !== true && !actionBusy) {
                    onSelectItem?.(item);
                  }
                }}
                onKeyDown={(event) => {
                  if ((event.key === "Enter" || event.key === " ") && item.is_dir !== true && !actionBusy) {
                    event.preventDefault();
                    setSelectedActionPath(actionKey);
                    onSelectItem?.(item);
                  }
                }}
                style={{
                  position: "relative",
                  display: "flex",
                  alignItems: "center",
                  gap: compact ? "6px" : "10px",
                  width: "100%",
                  border: "none",
                  background: selectedActionPath === actionKey
                    ? "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 14%, transparent), color-mix(in srgb, var(--accent-color) 6%, transparent))"
                    : "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 8%, transparent), color-mix(in srgb, var(--accent-color) 3%, transparent))",
                  padding: compact ? "6px 7px" : "6px 10px",
                  cursor: item.is_dir === true ? "default" : "pointer",
                  textAlign: "left",
                  borderRadius: "8px",
                  transition: "background 0.15s",
                  opacity: item.is_dir === true ? 0.72 : 1,
                }}
                onMouseEnter={(e) => {
                  setHoveredActionPath(actionKey);
                  e.currentTarget.style.background = "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 12%, transparent), color-mix(in srgb, var(--accent-color) 5%, transparent))";
                }}
                onMouseLeave={(e) => {
                  setHoveredActionPath((current) => current === actionKey ? "" : current);
                  e.currentTarget.style.background = selectedActionPath === actionKey
                    ? "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 14%, transparent), color-mix(in srgb, var(--accent-color) 6%, transparent))"
                    : "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 8%, transparent), color-mix(in srgb, var(--accent-color) 3%, transparent))";
                }}
              >
                <span style={{ width: compact ? "18px" : "24px", color: renderStatusColor(item.status), fontSize: "12px", fontWeight: 700, flexShrink: 0 }}>
                  {renderStatusLabel(item.status)}
                </span>
                <span title={item.display_path || item.path} style={{ flex: 1, minWidth: 0, fontSize: "12px", color: "var(--text-primary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {compactPath(item.display_path || item.path)}
                </span>
                <span style={{ display: "inline-flex", alignItems: "center", gap: compact ? "2px" : "8px", fontSize: "11px", color: "var(--text-secondary)", flexShrink: 0 }}>
                  {renderLineStat(item.additions, "+")}
                  {renderLineStat(item.deletions, "-")}
                </span>
                {showActions ? (
                  <span
                    style={{
                      position: "absolute",
                      top: "50%",
                      right: "4px",
                      transform: "translateY(-50%)",
                      display: "inline-flex",
                      alignItems: "center",
                      gap: 0,
                      padding: "2px",
                      borderRadius: "7px",
                      border: "1px solid var(--border-color)",
                      background: "var(--menu-bg)",
                      boxShadow: "0 6px 16px rgba(15, 23, 42, 0.14)",
                      zIndex: 3,
                    }}
                  >
                    <GitIconButton
                      title={t("git.openFile")}
                      disabled={!onOpenItem || item.is_dir === true || !!actionBusy}
                      onClick={() => onOpenItem?.(item)}
                    >
                      <OpenFileIcon />
                    </GitIconButton>
                    <GitIconButton
                      title={t("git.discardChanges")}
                      disabled={!onDiscardItem || !!actionBusy}
                      onClick={() => {
                        const runDiscard = () =>
                          runPanelAction(`discard:${item.path}`, () => onDiscardItem?.(item));
                        if (item.status !== "??") {
                          return runDiscard();
                        }
                        void confirmDialog({
                          message: t("git.confirmDeleteUntracked", { path: item.path }),
                          danger: true,
                        }).then((ok) => {
                          if (ok) {
                            return runDiscard();
                          }
                          return undefined;
                        });
                      }}
                    >
                      <UndoIcon />
                    </GitIconButton>
                    <GitIconButton
                      title={item.staged === true ? t("git.unstageChanges") : t("git.stageChanges")}
                      disabled={!onStageItem || !!actionBusy}
                      onClick={() => runPanelAction(`${item.staged === true ? "unstage" : "stage"}:${item.path}`, () => onStageItem?.(item))}
                    >
                      {item.staged === true ? <UnstageIcon /> : <StageIcon />}
                    </GitIconButton>
                  </span>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
