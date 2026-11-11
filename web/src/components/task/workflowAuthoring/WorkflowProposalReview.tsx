import { useI18n } from "../../../i18n/index";
import { panelButtonStyle } from "../../shell/PanelShell";
import type { WorkflowAuthoring } from "../useWorkflowAuthoring";
import type { WorkflowAuthoringApplyResult, WorkflowAuthoringReview } from "../useWorkflowAuthoringReview";
import { WorkflowProposalDiff } from "./WorkflowProposalDiff";
import { WorkflowProposalSummary } from "./WorkflowProposalSummary";

export type WorkflowProposalReviewProps = {
  authoring: WorkflowAuthoring;
  review: WorkflowAuthoringReview;
  disabled?: boolean;
  onApplied?: (result: WorkflowAuthoringApplyResult) => void;
};

// The single review surface: Agent summary, per-operation diff, selection and
// one CAS apply. Every disabled state has a visible reason; nothing here retries
// a stale proposal or rebases it onto a newer revision.
export function WorkflowProposalReview({ authoring, review, disabled, onApplied }: WorkflowProposalReviewProps) {
  const { t } = useI18n();
  const proposal = authoring.state.proposal;
  if (!proposal || authoring.state.phase !== "review") return null;
  const canApply = !disabled && review.canApply && review.subset.operations.length > 0;
  const apply = () => {
    if (!canApply) return;
    void review.apply().then((result) => {
      if (result) onApplied?.(result);
    });
  };

  return (
    <section data-testid="workflow-proposal-review" style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
      <WorkflowProposalSummary proposal={proposal} />
      {proposal.status === "stale" ? (
        <div data-testid="workflow-proposal-stale" role="status">
          {t("task.workflowAuthoring.staleNotice", { revision: authoring.state.target?.revision || "-" })}
        </div>
      ) : null}
      {proposal.status === "conflict" ? (
        <div data-testid="workflow-proposal-conflict" role="alert" style={{ color: "var(--danger-color,#ef4444)" }}>
          {t("task.workflowAuthoring.conflictNotice", { revision: authoring.state.target?.revision || "-" })}
        </div>
      ) : null}
      {proposal.status === "applied" ? (
        <div data-testid="workflow-proposal-applied" role="status">
          {t("task.workflowAuthoring.appliedNotice", { revision: authoring.state.target?.revision || "-" })}
        </div>
      ) : null}
      {authoring.state.error ? <div role="alert" style={{ color: "var(--danger-color,#ef4444)" }}>{authoring.state.error}</div> : null}
      <WorkflowProposalDiff
        proposal={proposal}
        selection={review.selection}
        subset={review.subset}
        onToggle={review.toggle}
        disabled={review.busy || proposal.status === "applying"}
      />
      <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
        <button type="button" style={panelButtonStyle("secondary")} onClick={review.selectAll} disabled={review.busy}>
          {t("task.workflowAuthoring.selectAll")}
        </button>
        <button type="button" style={panelButtonStyle("secondary")} onClick={review.clear} disabled={review.busy || review.selection.length === 0}>
          {t("task.workflowAuthoring.clearSelection")}
        </button>
        <button type="button" style={panelButtonStyle("primary")} data-testid="workflow-proposal-apply" disabled={!canApply} onClick={apply}>
          {review.busy || proposal.status === "applying"
            ? t("task.workflowAuthoring.applying")
            : t("task.workflowAuthoring.apply")}
        </button>
        {review.selection.length === 0 ? <span>{t("task.workflowAuthoring.noSelection")}</span> : null}
        {disabled ? <span>{t("task.workflowAuthoring.dirtyBlocked")}</span> : null}
      </div>
    </section>
  );
}