import { controlPath } from "../net/controlPlane";
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

/** Structured issues from a 422 workflow patch response; [] for other errors. */
export function workflowPatchIssues(error: unknown): WorkflowPatchIssue[] {
  const payload = (error as { payload?: { issues?: unknown } })?.payload;
  const issues = payload?.issues;
  return Array.isArray(issues) ? (issues as WorkflowPatchIssue[]) : [];
}
