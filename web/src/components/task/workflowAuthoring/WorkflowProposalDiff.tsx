import { useI18n } from "../../../i18n/index";
import type { WorkflowGraph } from "../../../shared/workflowModel";
import type { WorkflowAuthoringProposal } from "./proposalState";
import type { WorkflowSelectionResult } from "./proposalSelection";

type NodeShape = WorkflowGraph["nodes"][number];
type ProposalOperation = WorkflowAuthoringProposal["operations"][number];

export type WorkflowProposalDiffProps = {
  proposal: WorkflowAuthoringProposal;
  selection: string[];
  subset: WorkflowSelectionResult;
  onToggle: (operationID: string) => void;
  disabled?: boolean;
};

function nodeForOperation(op: ProposalOperation, proposed?: { before?: NodeShape; after?: NodeShape }): NodeShape | null {
  return proposed?.after ?? proposed?.before ?? op.node ?? null;
}

function describe(value: unknown): string {
  if (value === undefined) return "-";
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

// Every change the server reported, never a summary that hides edges, params
// or layout. Each operation is opt-in: the initial selection is empty.
export function WorkflowProposalDiff({ proposal, selection, subset, onToggle, disabled }: WorkflowProposalDiffProps) {
  const { t } = useI18n();
  const selected = new Set(selection);
  const proposed = new Map((proposal.validation?.operations ?? []).map((op) => [op.operation_id, op]));
  const dropped = new Map(subset.dropped.map((entry) => [entry.id, entry.reason]));
  const autoIncluded = new Set(subset.autoIncluded);

  if (proposal.operations.length === 0) {
    return <div data-testid="workflow-proposal-diff-empty">{t("task.workflowAuthoring.noOperations")}</div>;
  }

  return (
    <ul data-testid="workflow-proposal-diff" style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 6 }}>
      {proposal.operations.map((op) => {
        const diff = proposed.get(op.id);
        const node = nodeForOperation(op, diff);
        const edge = diff?.edge ?? op.edge;
        const dropReason = dropped.get(op.id);
        return (
          <li
            key={op.id}
            data-testid="workflow-proposal-op"
            data-operation-id={op.id}
            data-operation={op.op}
            data-dropped={dropReason || undefined}
            data-auto-included={autoIncluded.has(op.id) || undefined}
            style={{ border: "1px solid var(--border-color,#e5e7eb)", borderRadius: 6, padding: 6, opacity: dropReason ? 0.6 : 1 }}
          >
            <label style={{ display: "flex", gap: 6, alignItems: "flex-start", cursor: "pointer" }}>
              <input
                type="checkbox"
                checked={selected.has(op.id)}
                disabled={disabled}
                onChange={() => onToggle(op.id)}
              />
              <span style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                <span>
                  <strong>{t(`task.workflowAuthoring.op.${op.op}`)}</strong>
                  {op.node?.id || op.node_id ? <span> · {op.node?.id || op.node_id}</span> : null}
                </span>
                {edge ? <span data-field="edge">{edge.from} → {edge.to}{edge.route ? ` (${edge.route})` : ""}</span> : null}
                {node?.params ? <span data-field="params">{t("task.workflowAuthoring.params")}: {describe(node.params)}</span> : null}
                {node?.position ? <span data-field="position">{t("task.workflowAuthoring.position")}: {describe(node.position)}</span> : null}
                {dropReason ? (
                  <span data-testid="workflow-proposal-dropped" style={{ color: "var(--danger-color,#ef4444)" }}>
                    {t(`task.workflowAuthoring.dropped.${dropReason}`)}
                  </span>
                ) : null}
                {autoIncluded.has(op.id) ? <span>{t("task.workflowAuthoring.autoIncluded")}</span> : null}
              </span>
            </label>
          </li>
        );
      })}
    </ul>
  );
}