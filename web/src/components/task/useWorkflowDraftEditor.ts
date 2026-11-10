import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import {
  createWorkflowDraft,
  listWorkflowDrafts,
  publishWorkflowDraft,
  updateWorkflowDraft,
  type WorkflowDraft,
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
  setGraph: (graph: WorkflowGraph) => void;
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

  const selected = useMemo(
    () => drafts.find((d) => d.workflow_id === selectedId) || null,
    [drafts, selectedId],
  );

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

  const confirmLeave = useCallback(async () => {
    if (!dirty) return true;
    return confirmDialog({ message: t("task.workflowEditor.leaveConfirm"), danger: true });
  }, [dirty, t]);

  return { drafts, selected, selectedId, graph, revision, dirty, loading, saving, error, setGraph, choose, create, save, publish, confirmLeave };
}
