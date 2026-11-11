import type { WorkflowPatchIssue, WorkflowPatchOperation } from "../../../services/task/workflowDraft";

// Pure selection rules for a reviewed Agent proposal. The server stays the
// authority: this only removes combinations that are already known to dangle,
// then the selected subset is validated and dry-run again on the server.
export type WorkflowSelectionDrop = {
  id: string;
  reason: "dangling-edge" | "edge-touches-removed-node";
};

export type WorkflowSelectionResult = {
  operations: WorkflowPatchOperation[];
  autoIncluded: string[];
  dropped: WorkflowSelectionDrop[];
};

export function defaultWorkflowSelection(_proposal: unknown): string[] {
  return [];
}

function edgeEndpoints(op: WorkflowPatchOperation): [string, string] | null {
  const edge = op.edge;
  if (!edge || !edge.from || !edge.to) return null;
  return [edge.from, edge.to];
}

export function resolveWorkflowSelection(
  operations: WorkflowPatchOperation[],
  selectedIDs: string[],
): WorkflowSelectionResult {
  const selected = new Set(selectedIDs);
  const addedNodes = new Map<string, string>();
  for (const op of operations) {
    if (op.op === "add_node" && op.node?.id) addedNodes.set(op.node.id, op.id);
  }

  const included = new Set<string>();
  for (const op of operations) {
    if (selected.has(op.id)) included.add(op.id);
  }

  // Removing a node must not leave its edges behind. When the proposal carries
  // the matching edge removals, accepting the node removal accepts them too.
  const removedNodes = new Set<string>();
  for (const op of operations) {
    if (included.has(op.id) && op.op === "remove_future_node" && op.node_id) {
      removedNodes.add(op.node_id);
    }
  }
  const autoIncluded: string[] = [];
  for (const op of operations) {
    if (included.has(op.id) || op.op !== "remove_future_edge") continue;
    const endpoints = edgeEndpoints(op);
    if (endpoints && (removedNodes.has(endpoints[0]) || removedNodes.has(endpoints[1]))) {
      included.add(op.id);
      autoIncluded.push(op.id);
    }
  }

  const dropped: WorkflowSelectionDrop[] = [];
  for (const op of operations) {
    if (!included.has(op.id) || op.op !== "add_edge") continue;
    const endpoints = edgeEndpoints(op);
    if (!endpoints) continue;
    const rejectedEndpoint = endpoints.some((endpoint) => {
      const addNodeID = addedNodes.get(endpoint);
      return !!addNodeID && !included.has(addNodeID);
    });
    if (rejectedEndpoint) {
      included.delete(op.id);
      dropped.push({ id: op.id, reason: "dangling-edge" });
      continue;
    }
    if (endpoints.some((endpoint) => removedNodes.has(endpoint))) {
      included.delete(op.id);
      dropped.push({ id: op.id, reason: "edge-touches-removed-node" });
    }
  }

  return {
    operations: operations.filter((op) => included.has(op.id)),
    autoIncluded,
    dropped,
  };
}

type ApplyableProposal = {
  status: string;
  operations: WorkflowPatchOperation[];
  dryRun?: { status?: string; issues?: WorkflowPatchIssue[] };
};

// The backend dry-run reports failure through `issues`, never a status value
// (its status is only ever done/waiting_user). This is the single place that
// reads that verdict, so the review UI and the reducer agree on it.
export function workflowReviewIssues(
  review: { dry_run?: { issues?: WorkflowPatchIssue[] } } | null | undefined,
): WorkflowPatchIssue[] {
  return review?.dry_run?.issues ?? [];
}

export function canApplyWorkflowProposal(proposal: ApplyableProposal | null | undefined, selectedIDs: string[]): boolean {
  if (!proposal || proposal.status !== "ready") return false;
  if ((proposal.dryRun?.issues?.length ?? 0) > 0) return false;
  if (proposal.operations.length === 0 || selectedIDs.length === 0) return false;
  const known = new Set(proposal.operations.map((op) => op.id));
  return selectedIDs.some((id) => known.has(id));
}