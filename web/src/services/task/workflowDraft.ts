import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { WorkflowGraph, WorkflowVersion } from "../../shared/workflowModel";
import type { WorkflowDraft } from "../../shared/workflowModel";

export type { WorkflowDraft, WorkflowVersion };

export async function listWorkflowDrafts(nodeIdParam?: string): Promise<WorkflowDraft[]> {
  const payload = await protectedJSON<{ drafts?: WorkflowDraft[] }>(appURL("/api/workflows", undefined, nodeIdParam));
  return Array.isArray(payload?.drafts) ? payload.drafts : [];
}

type DraftEnvelope = { draft?: WorkflowDraft };

export async function createWorkflowDraft(
  input: { workflowId: string; rootId?: string; author?: string; graph: WorkflowGraph },
  nodeIdParam?: string,
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(appURL("/api/workflows", undefined, nodeIdParam), {
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
  nodeIdParam?: string,
): Promise<WorkflowDraft> {
  const payload = await protectedJSON<DraftEnvelope>(appURL(`/api/workflows/${encodeURIComponent(workflowId)}`, undefined, nodeIdParam), {
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
  nodeIdParam?: string,
): Promise<WorkflowVersion> {
  const payload = await protectedJSON<{ version?: WorkflowVersion }>(appURL(`/api/workflows/${encodeURIComponent(workflowId)}/publish`, undefined, nodeIdParam), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirmed_revision: input.revision, published_by: input.publishedBy || "user" }),
  });
  if (!payload?.version) throw new Error("publish: empty response");
  return payload.version;
}
