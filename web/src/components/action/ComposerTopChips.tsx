import React from "react";
import { WorktreeBranchSelector } from "../task/WorktreeBranchSelector";
import { NoWorktreeIcon } from "../common/NoWorktreeIcon";
import { CodexRateLimitIndicator } from "../agent/CodexRateLimitIndicator";
import { AgentMemoryIndicator } from "../agent/AgentMemoryIndicator";
import { useI18n } from "../../i18n";
import { appAccentHexToRgba } from "../../shared/actionBarUtils";
import type { GitBranchesPayload } from "../../shared/gitTypes";
import { useRootNodeId } from "./useRootNodeId";
import { COMPOSER_CHIP_FONT_SIZE } from "./composerStyles";

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
  const rootNodeId = useRootNodeId(currentRootId || "");
  const rgba = (alpha: number) => appAccentHexToRgba(accentHex, alpha);
  const hasBoundSession = !!currentSession;
  // 徽标带是流内的一行：选择器弹层打开时隐藏内容但保留行高，避免布局跳动。
  const contentHidden = agentSelectorOpen || modeSelectorOpen;

  return (
    <div
      data-onboarding="composer-top-chips"
      style={{
        position: "relative",
        width: "100%",
        minWidth: 0,
        minHeight: "24px",
        display: "flex",
        alignItems: "center",
        padding: isMobile ? "0 4px 4px" : "0 2px 4px",
        boxSizing: "border-box",
      }}
    >
      <div
        style={{
          display: contentHidden ? "none" : "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          gap: "8px",
          width: "100%",
          minWidth: 0,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0 }}>
          <AgentMemoryIndicator refreshToken={agentsVersion + codexRateLimitsRefreshToken} nodeId={rootNodeId} />
          {planModeActive ? (
            <div style={{ display: "inline-flex", alignItems: "center", gap: "5px", height: "20px", padding: "0 5px 0 8px", borderRadius: "999px", border: `1px solid ${rgba(0.22)}`, background: `linear-gradient(${rgba(0.10)}, ${rgba(0.10)}), var(--mobile-overlay-bg)`, color: accentHex, fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 700, lineHeight: 1, flexShrink: 0 }}>
              <span>Plan</span>
              <button type="button" aria-label={t("action.closePlanMode")} title={t("action.closePlanMode")} onMouseDown={(event) => event.preventDefault()} onClick={() => onTogglePlanMode(false)} style={{ width: "14px", height: "14px", border: "none", borderRadius: "999px", background: "transparent", color: "currentColor", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer", fontSize: "14px", lineHeight: 1, padding: 0 }}>
                <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" aria-hidden="true"><path d="M0 0h24v24H0z" fill="none" /><path fill="currentColor" fillRule="evenodd" d="M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0M7.293 16.707a1 1 0 0 1 0-1.414L10.586 12 7.293 8.707a1 1 0 0 1 1.414-1.414L12 10.586l3.293-3.293a1 1 0 0 1 1.414 1.414L13.414 12l3.293 3.293a1 1 0 0 1-1.414 1.414L12 13.414l-3.293 3.293a1 1 0 0 1-1.414 0" clipRule="evenodd" /></svg>
              </button>
            </div>
          ) : null}
          {!agentSelectorOpen && !modeSelectorOpen && !hasBoundSession && currentRootIsGitRepo ? (
            <>
              <button type="button" onClick={onToggleWorktree} disabled={sending} aria-label={createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")} title={createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")} style={{ height: "24px", borderRadius: "6px", border: createWorktree ? "1px solid rgba(22, 163, 74, 0.28)" : "1px solid var(--border-color)", background: createWorktree ? "linear-gradient(rgba(22, 163, 74, 0.08), rgba(22, 163, 74, 0.08)), var(--mobile-overlay-bg)" : "linear-gradient(rgba(100, 116, 139, 0.10), rgba(100, 116, 139, 0.10)), var(--mobile-overlay-bg)", color: createWorktree ? "#15803d" : "var(--text-secondary)", padding: createWorktree ? "0 8px" : "0 8px 0 5px", fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 800, cursor: sending ? "not-allowed" : "pointer", whiteSpace: "nowrap", display: "inline-flex", alignItems: "center", justifyContent: "center", gap: "3px" }}>
                {createWorktree ? "worktree" : <><NoWorktreeIcon size={12} />worktree</>}
              </button>
              {createWorktree ? <><WorktreeBranchSelector branchMode={worktreeBranchMode} branch={worktreeBranch} branches={worktreeBranches.branches} disabled={sending} maxWidth={isMobile ? 150 : 240} menuAlign={isMobile ? "left" : "right"} menuPlacement="top" onChange={onWorktreeBranchChange} />{worktreeBranchesLoading ? <span style={{ fontSize: "11px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>{t("common.loading")}</span> : worktreeBranchError ? <span title={worktreeBranchError} style={{ fontSize: "11px", color: "#b45309", whiteSpace: "nowrap" }}>{t("common.loadingFailed")}</span> : null}</> : null}
            </>
          ) : null}
          {mode !== "command" ? (
            <CodexRateLimitIndicator agent={agent} refreshToken={codexRateLimitsRefreshToken} nodeId={rootNodeId} />
          ) : null}
        </div>
      </div>
    </div>
  );
}
