import React from "react";
import { WorktreeBranchSelector } from "../WorktreeBranchSelector";
import { NoWorktreeIcon } from "../NoWorktreeIcon";
import { CodexRateLimitIndicator } from "../CodexRateLimitIndicator";
import { AgentMemoryIndicator } from "../AgentMemoryIndicator";
import { getRootNodeId } from "../../services/rootNode";
import { useI18n } from "../../i18n";
import { appAccentHexToRgba } from "./styleHelpers";
import { type GitBranchesPayload } from "../../services/git";

export function ComposerTopChips({
  planModeActive,
  planSessionKey,
  planRootId,
  agentSelectorOpen,
  modeSelectorOpen,
  isMobile,
  currentSession,
  currentRootIsGitRepo,
  mode,
  agent,
  accentHex,
  sending,
  createWorktree,
  worktreeBranchMode,
  worktreeBranch,
  worktreeBranches,
  worktreeBranchesLoading,
  worktreeBranchError,
  agentsVersion,
  codexRateLimitsRefreshToken,
  currentRootId,
  onTogglePlanMode,
  onToggleWorktree,
  onWorktreeBranchChange,
}: {
  planModeActive: boolean;
  planSessionKey: string;
  planRootId: string;
  agentSelectorOpen: boolean;
  modeSelectorOpen: boolean;
  isMobile: boolean;
  currentSession: unknown | null;
  currentRootIsGitRepo: boolean;
  mode: string;
  agent: string;
  accentHex: string;
  sending: boolean;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  worktreeBranches: GitBranchesPayload;
  worktreeBranchesLoading: boolean;
  worktreeBranchError: string;
  agentsVersion: number;
  codexRateLimitsRefreshToken: number;
  currentRootId: string | null | undefined;
  onTogglePlanMode: (enabled: boolean) => void;
  onToggleWorktree: () => void;
  onWorktreeBranchChange: (branchMode: "new" | "existing", branch: string) => void;
}) {
  const { t } = useI18n();
  const rgba = (alpha: number) => appAccentHexToRgba(accentHex, alpha);
  const hasBoundSession = !!currentSession;

  if (!(planModeActive || (!hasBoundSession && currentRootIsGitRepo && mode !== "command") || (mode !== "command" && agent === "codex"))) {
    return null;
  }

  return (
    <div style={{ position: "absolute", left: isMobile ? "4px" : "2px", right: isMobile ? "4px" : "8px", bottom: "calc(100% + 4px)", zIndex: 7, minWidth: 0, display: (agentSelectorOpen || modeSelectorOpen) ? "none" : "flex", alignItems: "flex-end", justifyContent: "space-between", gap: "8px", pointerEvents: "none" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0, pointerEvents: "auto" }}>
        {planModeActive ? (
          <div style={{ display: "inline-flex", alignItems: "center", gap: "5px", height: "20px", padding: "0 5px 0 8px", borderRadius: "999px", border: `1px solid ${rgba(0.22)}`, background: `linear-gradient(${rgba(0.10)}, ${rgba(0.10)}), var(--mobile-overlay-bg)`, color: accentHex, fontSize: "11px", fontWeight: 700, lineHeight: 1, flexShrink: 0 }}>
            <span>Plan</span>
            <button type="button" aria-label={t("action.closePlanMode")} title={t("action.closePlanMode")} onMouseDown={(event) => event.preventDefault()} onClick={() => onTogglePlanMode(false)} style={{ width: "14px", height: "14px", border: "none", borderRadius: "999px", background: "transparent", color: "currentColor", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer", fontSize: "14px", lineHeight: 1, padding: 0 }}>
              <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" aria-hidden="true"><path d="M0 0h24v24H0z" fill="none" /><path fill="currentColor" fillRule="evenodd" d="M21 12a9 9 0 1 1-18 0a9 9 0 0 1 18 0M7.293 16.707a1 1 0 0 1 0-1.414L10.586 12L7.293 8.707a1 1 0 0 1 1.414-1.414L12 10.586l3.293-3.293a1 1 0 1 1 1.414 1.414L13.414 12l3.293 3.293a1 1 0 0 1-1.414 1.414L12 13.414l-3.293 3.293a1 1 0 0 1-1.414 0" clipRule="evenodd" /></svg>
            </button>
          </div>
        ) : null}
        {!agentSelectorOpen && !modeSelectorOpen && !hasBoundSession && currentRootIsGitRepo ? (
          <>
            <button type="button" onClick={onToggleWorktree} disabled={sending} aria-label={createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")} title={createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")} style={{ height: "24px", borderRadius: "6px", border: createWorktree ? "1px solid rgba(22, 163, 74, 0.28)" : "1px solid var(--border-color)", background: createWorktree ? "linear-gradient(rgba(22, 163, 74, 0.08), rgba(22, 163, 74, 0.08)), var(--mobile-overlay-bg)" : "linear-gradient(rgba(100, 116, 139, 0.10), rgba(100, 116, 139, 0.10)), var(--mobile-overlay-bg)", color: createWorktree ? "#15803d" : "var(--text-secondary)", padding: createWorktree ? "0 8px" : "0 8px 0 5px", fontSize: "11px", fontWeight: 800, cursor: sending ? "not-allowed" : "pointer", whiteSpace: "nowrap", display: "inline-flex", alignItems: "center", justifyContent: "center", gap: "3px" }}>
              {createWorktree ? "worktree" : <><NoWorktreeIcon size={12} />worktree</>}
            </button>
            {createWorktree ? <><WorktreeBranchSelector branchMode={worktreeBranchMode} branch={worktreeBranch} branches={worktreeBranches.branches} disabled={sending} maxWidth={isMobile ? 150 : 240} menuAlign={isMobile ? "left" : "right"} menuPlacement="top" onChange={onWorktreeBranchChange} />{worktreeBranchesLoading ? <span style={{ fontSize: "11px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>{t("common.loading")}</span> : worktreeBranchError ? <span title={worktreeBranchError} style={{ fontSize: "11px", color: "#b45309", whiteSpace: "nowrap" }}>{t("common.loadingFailed")}</span> : null}</> : null}
          </>
        ) : null}
      </div>
      <div style={{ pointerEvents: "auto" }}><AgentMemoryIndicator refreshToken={agentsVersion + codexRateLimitsRefreshToken} /><CodexRateLimitIndicator agent={agent} refreshToken={codexRateLimitsRefreshToken} nodeId={getRootNodeId(currentRootId || "") as any} /></div>
    </div>
  );
}
