import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import {
  applyWorkflowDraftPatch,
  confirmWorkflowPublishProposal,
  createWorkflowDraft,
  listWorkflowDrafts,
  listWorkflowPublishProposals,
  publishWorkflowDraft,
  updateWorkflowDraft,
  validateWorkflowDraftPatch,
  workflowPatchIssues,
  type WorkflowDiff,
  type WorkflowDraft,
  type WorkflowPatch,
  type WorkflowPatchIssue,
  type WorkflowPublishProposal,
} from "../../services/task/workflowDraft";
import type { WorkflowGraph } from "../../shared/workflowModel";

// WorkflowDraftEditor 的取数与状态（.ts，允许 import services —— 视图 .tsx 不允许）。
//
// Draft/Version 都住控制面：service 层用 controlPath 打页面服务器，不随当前项目节点漂移。
// 所有写操作都带 CAS：保存带 base_revision，发布带 confirmed_revision（旧确认不能发布新内容）。
export type WorkflowDraftEditorState = {
  drafts: WorkflowDraft[];
  selected: WorkflowDraft | null;
  selectedId: string;
  graph: WorkflowGraph;
  revision: string;
  dirty: boolean;
  loading: boolean;
  saving: boolean;
  error: string;
  patchText: string;
  patchIssues: WorkflowPatchIssue[];
  patchDiff: WorkflowDiff | null;
  patchBusy: boolean;
  proposals: WorkflowPublishProposal[];
  proposalBusy: boolean;
  setGraph: (graph: WorkflowGraph) => void;
  setPatchText: (text: string) => void;
  validatePatch: () => Promise<void>;
  applyPatch: () => Promise<void>;
  refreshProposals: () => Promise<void>;
  confirmProposal: (proposal: WorkflowPublishProposal) => Promise<void>;
  choose: (draft: WorkflowDraft) => Promise<void>;
  create: () => Promise<void>;
  save: () => Promise<void>;
  publish: () => Promise<void>;
  confirmLeave: () => Promise<boolean>;
};

export function useWorkflowDraftEditor(onDirtyChange?: (dirty: boolean) => void): WorkflowDraftEditorState {
  const { t } = useI18n();
  const [drafts, setDrafts] = useState<WorkflowDraft[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [graph, setGraphState] = useState<WorkflowGraph>({ nodes: [], edges: [] });
  const [revision, setRevision] = useState("");
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [patchText, setPatchTextState] = useState("");
  const [patchIssues, setPatchIssues] = useState<WorkflowPatchIssue[]>([]);
  const [patchDiff, setPatchDiff] = useState<WorkflowDiff | null>(null);
  const [patchBusy, setPatchBusy] = useState(false);
  const [proposals, setProposals] = useState<WorkflowPublishProposal[]>([]);
  const [proposalBusy, setProposalBusy] = useState(false);

  const selected = useMemo(
    () => drafts.find((d) => d.workflow_id === selectedId) || null,
    [drafts, selectedId],
  );
  const selectedIdRef = useRef(selectedId);

  const refreshProposals = useCallback(async () => {
    const workflowId = selectedId;
    if (!workflowId) {
      setProposals([]);
      return;
    }
    try {
      const items = await listWorkflowPublishProposals(workflowId);
      if (selectedIdRef.current === workflowId) setProposals(items);
    } catch (e) {
      if (selectedIdRef.current === workflowId) setError(e instanceof Error ? e.message : String(e));
    }
  }, [selectedId]);

  useEffect(() => {
    selectedIdRef.current = selectedId;
    void refreshProposals();
  }, [selectedId, refreshProposals]);

  useEffect(() => {
    let cancelled = false;
    listWorkflowDrafts()
      .then((items) => {
        if (cancelled) return;
        setDrafts(items);
        if (items[0]) {
          setSelectedId(items[0].workflow_id);
          setGraphState(items[0].graph);
          setRevision(items[0].revision);
        }
      })
      .catch((e) => { if (!cancelled) setError(e instanceof Error ? e.message : String(e)); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => { onDirtyChange?.(dirty); }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!dirty) return;
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [dirty]);

  const setGraph = useCallback((next: WorkflowGraph) => {
    setGraphState(next);
    setDirty(true);
  }, []);

  const choose = useCallback(async (draft: WorkflowDraft) => {
    if (dirty && !(await confirmDialog({ message: t("task.workflowEditor.discardConfirm"), danger: true }))) return;
    setSelectedId(draft.workflow_id);
    setGraphState(draft.graph);
    setRevision(draft.revision);
    setDirty(false);
    setError("");
  }, [dirty, t]);

  const create = useCallback(async () => {
    const id = `workflow-${Date.now().toString(36)}`;
    const initial: WorkflowGraph = {
      nodes: [{ id: "start", name: t("task.workflowEditor.firstNode"), kind: "agent", params: { writes_workspace: true } }],
      edges: [],
    };
    setSaving(true);
    setError("");
    try {
      const draft = await createWorkflowDraft({ workflowId: id, graph: initial });
      setDrafts((old) => [...old, draft]);
      setSelectedId(draft.workflow_id);
      setGraphState(draft.graph);
      setRevision(draft.revision);
      setDirty(false);
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setSaving(false); }
  }, [t]);

  const save = useCallback(async () => {
    if (!selected) return;
    setSaving(true);
    setError("");
    try {
      const updated = await updateWorkflowDraft(selected.workflow_id, { baseRevision: revision, graph });
      setDrafts((old) => old.map((d) => (d.workflow_id === updated.workflow_id ? updated : d)));
      setRevision(updated.revision);
      setDirty(false);
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setSaving(false); }
  }, [selected, revision, graph]);

  const publish = useCallback(async () => {
    if (!selected || dirty) return;
    if (!(await confirmDialog({ message: t("task.workflowEditor.publishConfirm", { revision }) }))) return;
    setSaving(true);
    setError("");
    try {
      await publishWorkflowDraft(selected.workflow_id, { revision });
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setSaving(false); }
  }, [selected, dirty, revision, t]);

  const setPatchText = useCallback((text: string) => {
    setPatchTextState(text);
    setPatchIssues([]);
    setPatchDiff(null);
  }, []);

  const parsePatch = useCallback((): WorkflowPatch => {
    // Fail closed: never bind a pasted patch to the currently displayed
    // revision for the user. The Agent must state which revision it saw, so a
    // stale patch is rejected by the server CAS instead of silently applied.
    const parsed = JSON.parse(patchText) as Partial<WorkflowPatch>;
    if (Array.isArray(parsed) || !Array.isArray(parsed.operations) || !parsed.base_revision) {
      throw new Error(t("task.workflowEditor.patchInvalid"));
    }
    return {
      base_revision: parsed.base_revision,
      author: parsed.author || "user",
      operations: parsed.operations,
    };
  }, [patchText, t]);

  const validatePatch = useCallback(async () => {
    if (!selected || dirty) return;
    setPatchBusy(true);
    setError("");
    try {
      const patch = parsePatch();
      setPatchDiff(await validateWorkflowDraftPatch(selected.workflow_id, patch));
      setPatchIssues([]);
    } catch (e) {
      setPatchIssues(workflowPatchIssues(e));
      setError(e instanceof Error ? e.message : String(e));
    } finally { setPatchBusy(false); }
  }, [selected, dirty, parsePatch]);

  const applyPatch = useCallback(async () => {
    if (!selected || dirty) return;
    setPatchBusy(true);
    setError("");
    try {
      const patch = parsePatch();
      const result = await applyWorkflowDraftPatch(selected.workflow_id, patch);
      setPatchDiff(result.diff);
      setPatchIssues([]);
      setDrafts((old) => old.map((d) => (d.workflow_id === result.draft.workflow_id ? result.draft : d)));
      setGraphState(result.draft.graph);
      setRevision(result.draft.revision);
      setDirty(false);
    } catch (e) {
      setPatchIssues(workflowPatchIssues(e));
      setError(e instanceof Error ? e.message : String(e));
    } finally { setPatchBusy(false); }
  }, [selected, dirty, parsePatch]);

  const confirmProposal = useCallback(async (proposal: WorkflowPublishProposal) => {
    if (!selected || dirty || proposal.status !== "pending") return;
    if (!(await confirmDialog({
      message: t("task.workflowEditor.proposalConfirmMessage", { workflow: selected.workflow_id, revision: proposal.revision }),
    }))) return;
    setProposalBusy(true);
    setError("");
    try {
      await confirmWorkflowPublishProposal(proposal.id);
      await refreshProposals();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      await refreshProposals();
    } finally { setProposalBusy(false); }
  }, [selected, dirty, t, refreshProposals]);

  const confirmLeave = useCallback(async () => {
    if (!dirty) return true;
    return confirmDialog({ message: t("task.workflowEditor.leaveConfirm"), danger: true });
  }, [dirty, t]);

  return { drafts, selected, selectedId, graph, revision, dirty, loading, saving, error, patchText, patchIssues, patchDiff, patchBusy, proposals, proposalBusy, setGraph, setPatchText, validatePatch, applyPatch, refreshProposals, confirmProposal, choose, create, save, publish, confirmLeave };
}
