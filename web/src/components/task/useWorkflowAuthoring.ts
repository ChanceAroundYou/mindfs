import { useCallback, useReducer, useRef } from "react";
import { fetchSessionNamingPreference } from "../../services/prefs/preferences";
import {
  generateWorkflowCreateProposal,
  generateWorkflowDraftProposal,
  generateWorkflowRunProposal,
  workflowPatchIssues,
  type WorkflowDiff,
  type WorkflowPatchIssue,
} from "../../services/task/workflowDraft";
import {
  initialWorkflowAuthoringState,
  adoptConflictRevision,
  workflowAuthoringReducer,
  type WorkflowAuthoringScope,
  type WorkflowAuthoringState,
  type WorkflowAuthoringTarget,
  type WorkflowDryRunSnapshot,
} from "./workflowAuthoring/proposalState";

export type WorkflowAuthoringGenerateInput = {
  scope: WorkflowAuthoringScope;
  resourceID: string;
  revision: string;
  instruction: string;
  agent?: string;
  model?: string;
  rootID?: string;
  taskID?: string;
  nodeId?: string;
};

export type WorkflowAuthoringTransitions = {
  start: () => void;
  applied: (revision: string) => void;
  conflict: (currentRevision: string, error?: string) => void;
  failed: (error: string) => void;
  validation: (diff: WorkflowDiff, dryRun?: WorkflowDryRunSnapshot, issues?: WorkflowPatchIssue[], error?: string) => void;
};

export type WorkflowAuthoring = {
  state: WorkflowAuthoringState;
  generate: (input: WorkflowAuthoringGenerateInput) => Promise<{ conflictRevision?: string }>;
  observeRevision: (target: WorkflowAuthoringTarget) => void;
  cancel: () => void;
  reset: () => void;
  transitions: WorkflowAuthoringTransitions;
};

// Owns the authoring state machine and the generation request. It never applies
// or publishes: the reviewed patch goes through useWorkflowAuthoringReview and
// the existing CAS endpoints, after the user explicitly accepts operations.
export function useWorkflowAuthoring(): WorkflowAuthoring {
  const [state, dispatch] = useReducer(workflowAuthoringReducer, initialWorkflowAuthoringState);
  const stateRef = useRef(state);
  stateRef.current = state;
  const busyRef = useRef(false);

  const generate = useCallback(async (input: WorkflowAuthoringGenerateInput): Promise<{ conflictRevision?: string }> => {
    if (busyRef.current) {
      return {};
    }
    if (input.scope === "run" && (!input.rootID || !input.taskID)) {
      throw new Error("run authoring requires rootID and taskID");
    }
    busyRef.current = true;
    const target: WorkflowAuthoringTarget = {
      scope: input.scope,
      resourceID: input.resourceID,
      revision: input.revision,
    };
    const token = stateRef.current.token + 1;
    dispatch({ type: "begin", target });
    try {
      let agent = input.agent || "";
      let model = input.model || "";
      if (!agent) {
        const defaults = await fetchSessionNamingPreference().catch(() => null);
        if (defaults && !defaults.disabled) {
          agent = defaults.agent;
          model = model || defaults.model;
        }
      }
      if (!agent) {
        throw new Error("agent required");
      }
      const authorization = {
        baseRevision: input.revision,
        instruction: input.instruction,
        agent,
        model,
      };
      const response = input.scope === "create"
        ? await generateWorkflowCreateProposal(input.resourceID, authorization)
        : input.scope === "draft"
          ? await generateWorkflowDraftProposal(input.resourceID, authorization)
          : await generateWorkflowRunProposal(input.rootID!, input.taskID!, authorization, input.nodeId);
      if (!response.proposal) {
        dispatch({ type: "reject", token, issues: response.issues ?? [] });
        return {};
      }
      dispatch({ type: "resolve", token, response: response.proposal });
      if (response.diff) {
        dispatch({
          type: "validation",
          diff: response.diff,
          dryRun: response.dry_run,
          issues: response.issues ?? [],
        });
      }
      return {};
    } catch (error) {
      // A 409 means the snapshot moved under us. Adopt the revision the server
      // reported (then the editor reloads it) instead of leaving the stale base
      // in place for the next attempt to resend. A create has no live snapshot
      // to adopt: its only conflict is a duplicate id, which must be reworked.
      const { target: adopted, conflictRevision } = input.scope === "create"
        ? { target, conflictRevision: "" }
        : adoptConflictRevision(target, error);
      if (conflictRevision) {
        dispatch({ type: "observe-revision", target: adopted });
      }
      const issues = workflowPatchIssues(error);
      if (issues.length === 0) {
        issues.push({
          field: "revision",
          message: conflictRevision
            ? `workflow changed on the server (revision ${conflictRevision}); reload and generate again`
            : error instanceof Error ? error.message : String(error),
        });
      }
      dispatch({ type: "reject", token, issues });
      return { conflictRevision: conflictRevision || undefined };
    } finally {
      busyRef.current = false;
    }
  }, []);

  const observeRevision = useCallback((target: WorkflowAuthoringTarget) => {
    dispatch({ type: "observe-revision", target });
  }, []);

  const cancel = useCallback(() => {
    busyRef.current = false;
    dispatch({ type: "cancel" });
  }, []);

  const reset = useCallback(() => {
    busyRef.current = false;
    dispatch({ type: "reset" });
  }, []);

  const transitions: WorkflowAuthoringTransitions = {
    start: () => dispatch({ type: "apply-start" }),
    applied: (revision) => dispatch({ type: "apply-success", revision }),
    conflict: (currentRevision, error) => dispatch({ type: "apply-conflict", currentRevision, error }),
    failed: (error) => dispatch({ type: "apply-failure", error }),
    validation: (diff, dryRun, issues, error) => dispatch({ type: "validation", diff, dryRun, issues, error }),
  };

  return { state, generate, observeRevision, cancel, reset, transitions };
}