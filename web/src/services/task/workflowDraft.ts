import { controlPath } from "../net/controlPlane";
import { protectedJSON } from "../net/api";
import type { WorkflowGraph, WorkflowVersion } from "../../shared/workflowModel";
import type { WorkflowDraft } from "../../shared/workflowModel";

export type { WorkflowDraft, WorkflowVersion };

type DraftEnvelope = { draft?: WorkflowDraft };

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
