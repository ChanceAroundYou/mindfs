import {
  workflowPatchConflictRevision,
  type WorkflowPatchIssue,
  type WorkflowPatchOperation,
} from "../../../services/task/workflowDraft";
import {
  WORKFLOW_AUTHORING_MAX_OPERATIONS,
  type WorkflowAuthoringAction,
  type WorkflowAuthoringProposal,
  type WorkflowAuthoringState,
  type WorkflowAuthoringStatus,
  type WorkflowAuthoringTarget,
} from "./types";

export { WORKFLOW_AUTHORING_MAX_OPERATIONS } from "./types";
export type {
  WorkflowAuthoringAction,
  WorkflowAuthoringProposal,
  WorkflowAuthoringScope,
  WorkflowAuthoringState,
  WorkflowAuthoringStatus,
  WorkflowAuthoringTarget,
  WorkflowDryRunSnapshot,
} from "./types";

export const initialWorkflowAuthoringState: WorkflowAuthoringState = {
  phase: "idle",
  target: null,
  token: 0,
  proposal: null,
  error: null,
};

const OPERATIONS = new Set<WorkflowPatchOperation["op"]>([
  "add_node",
  "update_future_node",
  "remove_future_node",
  "add_edge",
  "remove_future_edge",
]);

const REQUIRED_FIELDS: Record<WorkflowPatchOperation["op"], Array<keyof WorkflowPatchOperation>> = {
  add_node: ["node"],
  update_future_node: ["node_id", "node"],
  remove_future_node: ["node_id"],
  add_edge: ["edge"],
  remove_future_edge: ["edge"],
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function sameTarget(left: WorkflowAuthoringTarget | null, right: WorkflowAuthoringTarget): boolean {
  return !!left && left.scope === right.scope && left.resourceID === right.resourceID;
}

function normalizeOperations(raw: unknown, issues: WorkflowPatchIssue[]): WorkflowPatchOperation[] {
  if (!Array.isArray(raw)) {
    issues.push({ field: "operations", message: "operations must be an array" });
    return [];
  }
  if (raw.length === 0) {
    issues.push({ field: "operations", message: "operations must contain at least one operation" });
    return [];
  }
  if (raw.length > WORKFLOW_AUTHORING_MAX_OPERATIONS) {
    issues.push({
      field: "operations",
      message: `operations exceeds the limit of ${WORKFLOW_AUTHORING_MAX_OPERATIONS}`,
    });
    return [];
  }

  const seen = new Set<string>();
  const operations: WorkflowPatchOperation[] = [];
  for (const entry of raw) {
    if (!isRecord(entry)) {
      issues.push({ field: "operations", message: "operation must be an object" });
      continue;
    }
    const id = typeof entry.id === "string" ? entry.id.trim() : "";
    if (!id) {
      issues.push({ field: "operations", message: "operation id is required" });
      continue;
    }
    if (seen.has(id)) {
      issues.push({ operation_id: id, field: "id", message: `duplicate operation id: ${id}` });
      continue;
    }
    const op = entry.op;
    if (typeof op !== "string" || !OPERATIONS.has(op as WorkflowPatchOperation["op"])) {
      issues.push({ operation_id: id, field: "op", message: `unknown operation: ${String(op)}` });
      continue;
    }
    const operation = entry as unknown as WorkflowPatchOperation;
    const missing = REQUIRED_FIELDS[operation.op].filter((field) => operation[field] === undefined);
    if (missing.length > 0) {
      issues.push({
        operation_id: id,
        field: missing[0],
        message: `${operation.op} requires ${missing.join(", ")}`,
      });
      continue;
    }
    seen.add(id);
    operations.push(operation);
  }
  return operations;
}

function invalidProposal(
  target: WorkflowAuthoringTarget,
  summary: string,
  rationale: string,
  baseRevision: string,
  operations: WorkflowPatchOperation[],
  issues: WorkflowPatchIssue[],
): WorkflowAuthoringProposal {
  return {
    scope: target.scope,
    resourceID: target.resourceID,
    baseRevision,
    summary,
    rationale,
    operations,
    issues,
    status: "invalid",
  };
}

function normalizeProposal(raw: unknown, target: WorkflowAuthoringTarget): WorkflowAuthoringProposal {
  if (!isRecord(raw)) {
    return invalidProposal(target, "", "", target.revision, [], [
      { field: "response", message: "response must be an object" },
    ]);
  }
  const issues: WorkflowPatchIssue[] = [];
  const summary = typeof raw.summary === "string" ? raw.summary.trim() : "";
  if (!summary) {
    issues.push({ field: "summary", message: "summary is required" });
  }
  const rationale = typeof raw.rationale === "string" ? raw.rationale : "";
  const rawBase = typeof raw.base_revision === "string" ? raw.base_revision.trim() : "";
  if (!rawBase) {
    issues.push({ field: "base_revision", message: "base_revision is required" });
  }
  const operations = normalizeOperations(raw.operations, issues);
  const baseRevision = rawBase || target.revision;
  const status: WorkflowAuthoringStatus =
    issues.length > 0 ? "invalid" : baseRevision !== target.revision ? "stale" : "preview";
  return {
    scope: target.scope,
    resourceID: target.resourceID,
    baseRevision,
    summary,
    rationale,
    operations,
    issues,
    status,
  };
}

function markStale(proposal: WorkflowAuthoringProposal | null): WorkflowAuthoringProposal | null {
  if (!proposal || proposal.status === "applied") {
    return proposal;
  }
  return { ...proposal, status: "stale" };
}

// A generation request carries the revision it read. When the server answers
// 409, the snapshot it used is gone: adopt the authoritative revision the
// server reported so the next attempt cannot resend the same stale base.
// Nothing is rebased — the caller still has to reload and generate again.
export function adoptConflictRevision(
  target: WorkflowAuthoringTarget,
  error: unknown,
): { target: WorkflowAuthoringTarget; conflictRevision: string } {
  const conflictRevision = workflowPatchConflictRevision(error);
  if (!conflictRevision) return { target, conflictRevision: "" };
  return { target: { ...target, revision: conflictRevision }, conflictRevision };
}

export function workflowAuthoringReducer(
  state: WorkflowAuthoringState,
  action: WorkflowAuthoringAction,
): WorkflowAuthoringState {
  switch (action.type) {
    case "begin":
      return {
        phase: "generating",
        target: { ...action.target },
        token: state.token + 1,
        proposal: null,
        error: null,
      };
    case "observe-revision": {
      if (!sameTarget(state.target, action.target) || state.target?.revision === action.target.revision) {
        return state;
      }
      return {
        ...state,
        target: { ...state.target!, revision: action.target.revision },
        proposal: markStale(state.proposal),
      };
    }
    case "resolve": {
      if (action.token !== state.token || !state.target) {
        return state;
      }
      return { ...state, phase: "review", proposal: normalizeProposal(action.response, state.target), error: null };
    }
    case "reject": {
      if (action.token !== state.token || !state.target) {
        return state;
      }
      return {
        ...state,
        phase: "review",
        proposal: {
          scope: state.target.scope,
          resourceID: state.target.resourceID,
          baseRevision: state.target.revision,
          summary: "",
          rationale: "",
          operations: [],
          issues: action.issues,
          status: "invalid",
        },
        error: null,
      };
    }
    case "fail":
      if (action.token !== state.token) {
        return state;
      }
      return { ...state, phase: "idle", token: state.token + 1, proposal: null, error: action.error };
    case "cancel":
    case "reset":
      return { ...initialWorkflowAuthoringState, token: state.token + 1 };
    case "validation": {
      if (!state.proposal || state.proposal.status === "stale") {
        return state;
      }
      const issues = [...(action.issues ?? []), ...(action.dryRun?.issues ?? [])];
      return {
        ...state,
        proposal: {
          ...state.proposal,
          validation: action.diff,
          dryRun: action.dryRun,
          issues,
          status: issues.length > 0 ? "invalid" : "ready",
        },
        error: action.error ?? state.error,
      };
    }
    case "apply-start":
      if (!state.proposal || state.proposal.status !== "ready") {
        return state;
      }
      return { ...state, proposal: { ...state.proposal, status: "applying" } };
    case "apply-success":
      if (!state.proposal || state.proposal.status !== "applying") {
        return state;
      }
      return {
        ...state,
        target: state.target ? { ...state.target, revision: action.revision } : null,
        proposal: { ...state.proposal, status: "applied" },
      };
    case "apply-conflict":
      if (!state.proposal || state.proposal.status !== "applying") {
        return state;
      }
      return {
        ...state,
        target: state.target ? { ...state.target, revision: action.currentRevision } : null,
        proposal: { ...state.proposal, status: "conflict" },
        error: action.error ?? state.error,
      };
    case "apply-failure":
      if (!state.proposal || state.proposal.status !== "applying") {
        return state;
      }
      return { ...state, proposal: { ...state.proposal, status: "error" }, error: action.error };
    default:
      return state;
  }
}