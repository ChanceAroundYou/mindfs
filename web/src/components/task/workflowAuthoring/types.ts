import type {
  WorkflowDiff,
  WorkflowPatchIssue,
  WorkflowPatchOperation,
} from "../../../services/task/workflowDraft";

// Mirrors kanban.WorkflowPatchMaxOperations. The server remains authoritative;
// this bound only keeps an oversized Agent response out of the review state.
export const WORKFLOW_AUTHORING_MAX_OPERATIONS = 200;

export type WorkflowAuthoringScope = "draft" | "run";

export type WorkflowAuthoringTarget = {
  scope: WorkflowAuthoringScope;
  resourceID: string;
  revision: string;
};

export type WorkflowDryRunSnapshot = {
  status?: string;
  steps?: unknown[];
  issues?: WorkflowPatchIssue[];
};

export type WorkflowAuthoringStatus =
  | "preview"
  | "stale"
  | "invalid"
  | "ready"
  | "applying"
  | "applied"
  | "conflict"
  | "error";

// The proposal is always bound to the target that was read, never to fields the
// response claims. A response cannot redirect review to another Draft or Run.
export type WorkflowAuthoringProposal = {
  scope: WorkflowAuthoringScope;
  resourceID: string;
  baseRevision: string;
  summary: string;
  rationale: string;
  operations: WorkflowPatchOperation[];
  validation?: WorkflowDiff;
  dryRun?: WorkflowDryRunSnapshot;
  issues: WorkflowPatchIssue[];
  status: WorkflowAuthoringStatus;
};

export type WorkflowAuthoringState = {
  phase: "idle" | "generating" | "review";
  target: WorkflowAuthoringTarget | null;
  token: number;
  proposal: WorkflowAuthoringProposal | null;
  error: string | null;
};

export type WorkflowAuthoringAction =
  | { type: "begin"; target: WorkflowAuthoringTarget }
  | { type: "observe-revision"; target: WorkflowAuthoringTarget }
  | { type: "resolve"; token: number; response: unknown }
  | { type: "reject"; token: number; issues: WorkflowPatchIssue[] }
  | { type: "fail"; token: number; error: string }
  | { type: "cancel" }
  | { type: "reset" }
  | {
      type: "validation";
      diff: WorkflowDiff;
      dryRun?: WorkflowDryRunSnapshot;
      issues?: WorkflowPatchIssue[];
      error?: string;
    }
  | { type: "apply-start" }
  | { type: "apply-success"; revision: string }
  | { type: "apply-conflict"; currentRevision: string; error?: string }
  | { type: "apply-failure"; error: string };