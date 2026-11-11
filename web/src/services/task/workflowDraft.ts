import { controlPath } from "../net/controlPlane";
import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { WorkflowGraph, WorkflowVersion } from "../../shared/workflowModel";
import type { WorkflowDraft } from "../../shared/workflowModel";

export type { WorkflowDraft, WorkflowVersion };

type DraftEnvelope = { draft?: WorkflowDraft };

// Typed Agent patch: the server accepts only structural operations, never an
// arbitrary JSON-path write. The same shape is returned by /snapshot for an
// Agent to read and by /patch/validate for a human to review before applying.
export type WorkflowPatchOperation = {
  id: string;
  op: "add_node" | "update_future_node" | "remove_future_node" | "add_edge" | "remove_future_edge";
  node?: WorkflowGraph["nodes"][number];
  node_id?: string;
  edge?: NonNullable<WorkflowGraph["edges"]>[number];
};

export type WorkflowPatch = {
  base_revision: string;
  author?: string;
  operations: WorkflowPatchOperation[];
};

export type WorkflowPatchIssue = {
  operation_id?: string;
  node_id?: string;
  field?: string;
  message: string;
};

export type WorkflowDiffOperation = {
  operation_id: string;
  op: string;
  node_id?: string;
  edge?: NonNullable<WorkflowGraph["edges"]>[number];
  before?: WorkflowGraph["nodes"][number];
  after?: WorkflowGraph["nodes"][number];
};

export type WorkflowDiff = {
  base_revision?: string;
  operations: WorkflowDiffOperation[];
};

export type WorkflowPatchLimits = {
  max_operations: number;
  max_nodes: number;
  max_node_bytes: number;
};

// Dry-run output of the authoring gateway: a static simulation that never
// starts a session or writes run state.
export type WorkflowDryRunPayload = {
  status?: string;
  steps?: unknown[];
  issues?: WorkflowPatchIssue[];
};

// The gateway returns the untrusted proposal together with the server's
// validate diff and dry-run. A missing diff means validation failed closed.
export type WorkflowAuthoringResponse = {
  proposal?: {
    summary: string;
    rationale?: string;
    base_revision: string;
    operations: WorkflowPatchOperation[];
  };
  diff?: WorkflowDiff;
  issues?: WorkflowPatchIssue[];
  dry_run?: WorkflowDryRunPayload;
};

// A reviewable publish proposal pins one exact Draft revision. It is not a
// published version; only a human confirmation turns it into one.
export type WorkflowPublishProposal = {
  id: string;
  workflow_id: string;
  revision: string;
  proposer: string;
  status: "pending" | "published" | "stale";
  version?: string;
  confirmed_by?: string;
  created_at: string;
  confirmed_at?: string;
};

// Workflow Drafts/Versions are control-plane data: route to page server regardless of selected node.
export async function listWorkflowDrafts(): Promise<WorkflowDraft[]> {
  const payload = await protectedJSON<{ items?: WorkflowDraft[] }>(controlPath("/api/workflows"));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function createWorkflowDraft(
  input: { workflowId: string; rootId?: string; author?: string; graph: WorkflowGraph },
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(controlPath("/api/workflows"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ workflow_id: input.workflowId, root_id: input.rootId || "", author: input.author || "user", graph: input.graph }),
  });
  if (!payload?.draft) throw new Error("draft create: empty response");
  return payload.draft;
}

export async function updateWorkflowDraft(
  workflowId: string,
  input: { baseRevision: string; author?: string; graph: WorkflowGraph },
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(controlPath(`/api/workflows/${encodeURIComponent(workflowId)}`), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ base_revision: input.baseRevision, author: input.author || "user", graph: input.graph }),
  });
  if (!payload?.draft) throw new Error("draft update: empty response");
  return payload.draft;
}

export async function publishWorkflowDraft(
  workflowId: string,
  input: { revision: string; publishedBy?: string },
): Promise<WorkflowVersion> {
  const payload = await protectedJSON<{ version?: WorkflowVersion }>(controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/publish`), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirmed_revision: input.revision, published_by: input.publishedBy || "user" }),
  });
  if (!payload?.version) throw new Error("publish: empty response");
  return payload.version;
}

export async function validateWorkflowDraftPatch(workflowId: string, patch: WorkflowPatch): Promise<WorkflowDiff> {
  const payload = await protectedJSON<{ diff?: WorkflowDiff }>(controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/patch/validate`), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
  if (!payload?.diff) throw new Error("patch validate: empty response");
  return payload.diff;
}

export async function applyWorkflowDraftPatch(
  workflowId: string,
  patch: WorkflowPatch,
): Promise<{ draft: WorkflowDraft; diff: WorkflowDiff }> {
  const payload = await protectedJSON<{ draft?: WorkflowDraft; diff?: WorkflowDiff }>(controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/patch/apply`), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
  if (!payload?.draft || !payload.diff) throw new Error("patch apply: empty response");
  return { draft: payload.draft, diff: payload.diff };
}

export async function listWorkflowPublishProposals(workflowId: string): Promise<WorkflowPublishProposal[]> {
  const payload = await protectedJSON<{ items?: WorkflowPublishProposal[] }>(
    controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/publish-proposals`),
  );
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function confirmWorkflowPublishProposal(
  proposalId: string,
): Promise<{ version: WorkflowVersion; proposal: WorkflowPublishProposal }> {
  const payload = await protectedJSON<{ version?: WorkflowVersion; proposal?: WorkflowPublishProposal }>(
    controlPath(`/api/workflows/publish-proposals/${encodeURIComponent(proposalId)}/confirm`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ published_by: "user" }),
    },
  );
  if (!payload?.version || !payload.proposal) throw new Error("proposal confirm: empty response");
  return { version: payload.version, proposal: payload.proposal };
}

export type WorkflowAuthoringGenerateInput = {
  baseRevision: string;
  instruction: string;
  agent: string;
  model?: string;
};

// Generation is read-only: the gateway returns a candidate plus the server's
// validate/dry-run. Applying is a separate, human-confirmed CAS action.
export async function generateWorkflowDraftProposal(
  workflowId: string,
  input: WorkflowAuthoringGenerateInput,
): Promise<WorkflowAuthoringResponse> {
  return protectedJSON<WorkflowAuthoringResponse>(
    controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/authoring/proposals`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        base_revision: input.baseRevision,
        instruction: input.instruction,
        agent: input.agent,
        model: input.model || "",
      }),
    },
  );
}

export async function generateWorkflowRunProposal(
  rootId: string,
  taskId: string,
  input: WorkflowAuthoringGenerateInput,
  nodeId?: string,
): Promise<WorkflowAuthoringResponse> {
  const params = new URLSearchParams({ root_id: rootId });
  return protectedJSON<WorkflowAuthoringResponse>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run/authoring/proposals`, params, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        base_revision: input.baseRevision,
        instruction: input.instruction,
        agent: input.agent,
        model: input.model || "",
      }),
    },
  );
}

export type WorkflowPatchReview = {
  diff: WorkflowDiff;
  dry_run?: WorkflowDryRunPayload;
};

export type WorkflowRunPatchResult = {
  run: { revision: number };
  diff: WorkflowDiff;
};

// Re-check exactly the operations a human accepted. The server rebuilds the
// candidate graph and statically dry-runs it, so a partial subset never relies
// on a client-side copy of the graph.
export async function reviewWorkflowDraftPatch(workflowId: string, patch: WorkflowPatch): Promise<WorkflowPatchReview> {
  const payload = await protectedJSON<WorkflowPatchReview>(
    controlPath(`/api/workflows/${encodeURIComponent(workflowId)}/patch/validate`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  if (!payload?.diff) throw new Error("patch review: empty response");
  return payload;
}

export async function reviewWorkflowRunPatch(
  rootId: string,
  taskId: string,
  patch: WorkflowPatch,
  nodeId?: string,
): Promise<WorkflowPatchReview> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<WorkflowPatchReview>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run/patch/validate`, params, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  if (!payload?.diff) throw new Error("run patch review: empty response");
  return payload;
}

export async function applyWorkflowRunPatch(
  rootId: string,
  taskId: string,
  patch: WorkflowPatch,
  nodeId?: string,
): Promise<WorkflowRunPatchResult> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<WorkflowRunPatchResult>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run/patch/apply`, params, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  if (!payload?.run || !payload.diff) throw new Error("run patch apply: empty response");
  return payload;
}

/** Current revision carried by a CAS conflict payload; "" for other errors. */
export function workflowPatchConflictRevision(error: unknown): string {
  const payload = (error as { payload?: { current_revision?: unknown } })?.payload;
  const revision = payload?.current_revision;
  if (typeof revision === "string") return revision;
  if (typeof revision === "number") return String(revision);
  return "";
}

/** Structured issues from a 422 workflow patch response; [] for other errors. */
export function workflowPatchIssues(error: unknown): WorkflowPatchIssue[] {
  const payload = (error as { payload?: { issues?: unknown } })?.payload;
  const issues = payload?.issues;
  return Array.isArray(issues) ? (issues as WorkflowPatchIssue[]) : [];
}
