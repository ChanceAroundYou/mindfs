import { useI18n } from "../../../i18n/index";
import type { WorkflowAuthoringProposal } from "./proposalState";

export type WorkflowProposalSummaryProps = {
  proposal: WorkflowAuthoringProposal;
};

// Read-only explanation of what the Agent proposed and why. Status is shown
// verbatim: "preview" is not a synonym for "ready to apply".
export function WorkflowProposalSummary({ proposal }: WorkflowProposalSummaryProps) {
  const { t } = useI18n();
  return (
    <section data-testid="workflow-proposal-summary" style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <strong>{proposal.summary || t("task.workflowAuthoring.untitled")}</strong>
        <span data-testid="workflow-proposal-status" data-status={proposal.status}>
          {t(`task.workflowAuthoring.status.${proposal.status}`)}
        </span>
        <span>{t("task.workflowAuthoring.baseRevision", { revision: proposal.baseRevision })}</span>
        <span>{t("task.workflowAuthoring.operationCount", { count: proposal.operations.length })}</span>
        {proposal.dryRun?.status ? (
          <span data-testid="workflow-proposal-dryrun">
            {t("task.workflowAuthoring.dryRun", { status: proposal.dryRun.status })}
          </span>
        ) : null}
      </div>
      {proposal.rationale ? <p style={{ margin: 0, opacity: 0.8 }}>{proposal.rationale}</p> : null}
      {proposal.issues.length > 0 ? (
        <ul data-testid="workflow-proposal-issues" style={{ margin: 0, paddingLeft: 18, color: "var(--danger-color,#ef4444)" }}>
          {proposal.issues.map((issue, index) => (
            <li key={`${issue.operation_id || "op"}-${issue.field || "field"}-${index}`}>
              {t("task.workflowEditor.patchIssueLine", { operation: issue.operation_id || "-", field: issue.field || "-", message: issue.message })}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}