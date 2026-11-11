import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  applyWorkflowDraftPatch,
  applyWorkflowRunPatch,
  reviewWorkflowDraftPatch,
  reviewWorkflowRunPatch,
  workflowPatchConflictRevision,
  workflowPatchIssues,
  type WorkflowDraft,
  type WorkflowPatch,
} from "../../services/task/workflowDraft";
import type { WorkflowAuthoring, WorkflowAuthoringGenerateInput } from "./useWorkflowAuthoring";
import { resolveWorkflowSelection, workflowReviewIssues, type WorkflowSelectionResult } from "./workflowAuthoring/proposalSelection";

export type WorkflowAuthoringApplyResult = {
  scope: "draft" | "run";
  revision: string;
  draft?: WorkflowDraft;
};

export type WorkflowAuthoringReview = {
  selection: string[];
  subset: WorkflowSelectionResult;
  canApply: boolean;
  busy: boolean;
  toggle: (operationID: string) => void;
  selectAll: () => void;
  clear: () => void;
  apply: () => Promise<WorkflowAuthoringApplyResult | null>;
};

type ReviewContext = Pick<WorkflowAuthoringGenerateInput, "rootID" | "taskID" | "nodeId">;

// Review is a separate step from generation so the generation path cannot
// persist anything. Applying is one CAS request per click: the selected subset
// is revalidated and dry-run on the server first, and a conflict is reported
// without retrying or rebasing.
export function useWorkflowAuthoringReview(
  authoring: WorkflowAuthoring,
  context: ReviewContext,
): WorkflowAuthoringReview {
  const [selection, setSelection] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const authoringRef = useRef(authoring);
  authoringRef.current = authoring;
  const contextRef = useRef(context);
  contextRef.current = context;
  const selectionRef = useRef(selection);
  selectionRef.current = selection;
  const busyRef = useRef(false);

  const token = authoring.state.token;
  useEffect(() => {
    setSelection([]);
  }, [token]);

  const operations = authoring.state.proposal?.operations;
  const subset = useMemo(
    () => resolveWorkflowSelection(operations ?? [], selection),
    [operations, selection],
  );

  const toggle = useCallback((operationID: string) => {
    setSelection((old) => old.includes(operationID)
      ? old.filter((id) => id !== operationID)
      : [...old, operationID]);
  }, []);

  const selectAll = useCallback(() => {
    setSelection((authoringRef.current.state.proposal?.operations ?? []).map((op) => op.id));
  }, []);

  const clear = useCallback(() => setSelection([]), []);

  const apply = useCallback(async (): Promise<WorkflowAuthoringApplyResult | null> => {
    if (busyRef.current) return null;
    const proposal = authoringRef.current.state.proposal;
    const target = authoringRef.current.state.target;
    if (!proposal || !target || proposal.status !== "ready") return null;
    const selected = resolveWorkflowSelection(proposal.operations, selectionRef.current);
    if (selected.operations.length === 0) return null;
    const patch: WorkflowPatch = {
      base_revision: proposal.baseRevision,
      author: "user",
      operations: selected.operations,
    };
    busyRef.current = true;
    setBusy(true);
    authoringRef.current.transitions.start();
    try {
      const review = target.scope === "draft"
        ? await reviewWorkflowDraftPatch(target.resourceID, patch)
        : await reviewWorkflowRunPatch(
          contextRef.current.rootID || "",
          contextRef.current.taskID || "",
          patch,
          contextRef.current.nodeId,
        );
      // The server reports dry-run failure as issues (200 + waiting_user), not
      // as a status. Blocking here is what stops an invalid subset from ever
      // reaching the CAS apply below.
      const dryRunIssues = workflowReviewIssues(review);
      if (dryRunIssues.length > 0) {
        authoringRef.current.transitions.validation(review.diff, review.dry_run, dryRunIssues);
        return null;
      }
      if (target.scope === "draft") {
        const result = await applyWorkflowDraftPatch(target.resourceID, patch);
        authoringRef.current.transitions.applied(result.draft.revision);
        return { scope: target.scope, revision: result.draft.revision, draft: result.draft };
      }
      const result = await applyWorkflowRunPatch(
        contextRef.current.rootID || "",
        contextRef.current.taskID || "",
        patch,
        contextRef.current.nodeId,
      );
      authoringRef.current.transitions.applied(String(result.run.revision));
      return { scope: target.scope, revision: String(result.run.revision) };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const currentRevision = workflowPatchConflictRevision(error);
      if (currentRevision) {
        authoringRef.current.transitions.conflict(currentRevision, message);
        return null;
      }
      authoringRef.current.transitions.failed(message);
      const issues = workflowPatchIssues(error);
      if (issues.length > 0) {
        authoringRef.current.transitions.validation({ operations: [] }, undefined, issues, message);
      }
      return null;
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, []);

  return {
    selection,
    subset,
    canApply: proposalReady(authoring) && selection.length > 0 && !busy,
    busy,
    toggle,
    selectAll,
    clear,
    apply,
  };
}

function proposalReady(authoring: WorkflowAuthoring): boolean {
  return authoring.state.proposal?.status === "ready";
}