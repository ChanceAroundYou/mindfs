import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import {
  createWorkflowDraft,
  deleteWorkflowDraft,
  listWorkflowDrafts,
  publishWorkflowDraft,
  updateWorkflowDraft,
  updateWorkflowMeta,
  type WorkflowDraft,
} from "../../services/task/workflowDraft";
import { workflowDraftDisplayName, type WorkflowGraph } from "../../shared/workflowModel";

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
  deletingId: string;
  metaBusy: boolean;
  setGraph: (graph: WorkflowGraph) => void;
  updateMeta: (draft: WorkflowDraft, input: { name: string; description: string }) => Promise<boolean>;
  choose: (draft: WorkflowDraft) => Promise<void>;
  reloadSelected: () => Promise<void>;
  create: () => Promise<void>;
  remove: (draft: WorkflowDraft) => Promise<void>;
  save: () => Promise<void>;
  revert: () => void;
  adoptDraft: (draft: WorkflowDraft) => void;
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
  const [deletingId, setDeletingId] = useState("");
  const [error, setError] = useState("");
  const [metaBusy, setMetaBusy] = useState(false);

  const selected = useMemo(
    () => drafts.find((d) => d.workflow_id === selectedId) || null,
    [drafts, selectedId],
  );
  const selectedIdRef = useRef(selectedId);

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

  // A generation 409 means the server moved past the revision this canvas was
  // read at. Re-read the draft list and adopt the selected draft's fresh graph
  // instead of keeping a canvas the server no longer agrees with.
  const reloadSelected = useCallback(async () => {
    const workflowId = selectedIdRef.current;
    if (!workflowId) return;
    try {
      const items = await listWorkflowDrafts();
      setDrafts(items);
      const fresh = items.find((d) => d.workflow_id === workflowId);
      if (fresh) {
        setGraphState(fresh.graph);
        setRevision(fresh.revision);
        setDirty(false);
      }
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  }, []);

  const create = useCallback(async () => {
    const id = `workflow-${Date.now().toString(36)}`;
    const initial: WorkflowGraph = {
      // 空白工作流也要能直接跑：没有 prompt 的 agent 节点在后端是配置错误
      // （requires params.prompt），用户点了发布才发现跑不起来。
      nodes: [{ id: "start", name: t("task.workflowEditor.firstNode"), kind: "agent", params: { prompt: "{previous_input}", writes_workspace: true } }],
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

  const remove = useCallback(async (draft: WorkflowDraft) => {
    const displayName = workflowDraftDisplayName(draft);
    if (!(await confirmDialog({
      message: t("task.workflowEditor.deleteConfirm", { workflow: displayName }),
      danger: true,
    }))) return;
    setDeletingId(draft.workflow_id);
    setError("");
    try {
      await deleteWorkflowDraft(draft.workflow_id);
      const remaining = drafts.filter((item) => item.workflow_id !== draft.workflow_id);
      setDrafts(remaining);
      if (selectedIdRef.current === draft.workflow_id) {
        const next = remaining[0];
        setSelectedId(next?.workflow_id ?? "");
        setGraphState(next?.graph ?? { nodes: [], edges: [] });
        setRevision(next?.revision ?? "");
        setDirty(false);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setDeletingId("");
    }
  }, [drafts, t]);

  // 保存即发布（用户 2026-11-20 裁定）：先写草稿拿到新修订，再拿这个新修订静默
  // 发布一次。界面上没有发布按钮、也没有版本概念，但执行侧仍只认已发布版本
  // （ActivityService 解析 Run 的 graph），所以这一步不能省。
  // 发布失败时草稿已经落库，保留 dirty 让「保存」还能再点一次（重试会写同一份图、
  // 再发布一次），错误照常显示。
  const save = useCallback(async () => {
    if (!selected) return;
    setSaving(true);
    setError("");
    try {
      const updated = await updateWorkflowDraft(selected.workflow_id, { baseRevision: revision, graph });
      setDrafts((old) => old.map((d) => (
        d.workflow_id === updated.workflow_id
          ? { ...updated, name: updated.name || d.name, description: updated.description ?? d.description }
          : d
      )));
      setRevision(updated.revision);
      try {
        await publishWorkflowDraft(updated.workflow_id, { revision: updated.revision });
        setDirty(false);
      } catch (e) {
        setDirty(true);
        setError(e instanceof Error ? e.message : String(e));
      }
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setSaving(false); }
  }, [selected, revision, graph]);

  // 还原：丢掉本地未保存改动，回到服务端那份草稿。不写库。
  const revert = useCallback(() => {
    if (!selected) return;
    setGraphState(selected.graph);
    setDirty(false);
    setError("");
  }, [selected]);

  // 名称/描述住在 WorkflowMeta；只改元数据，不动草稿修订，所以不会把画布弄脏。
  const updateMeta = useCallback(async (draft: WorkflowDraft, input: { name: string; description: string }) => {
    const name = input.name.trim();
    if (!name) return false;
    setMetaBusy(true);
    setError("");
    try {
      const meta = await updateWorkflowMeta(draft.workflow_id, { name, description: input.description.trim() });
      setDrafts((old) => old.map((item) => (
        item.workflow_id === draft.workflow_id ? { ...item, name: meta.name, description: meta.description } : item
      )));
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return false;
    } finally {
      setMetaBusy(false);
    }
  }, []);

  // The authoring apply returned an authoritative draft from the server; adopt
  // exactly that revision so the canvas never merges a second graph locally.
  const adoptDraft = useCallback((draft: WorkflowDraft) => {
    setDrafts((old) => (
      old.some((item) => item.workflow_id === draft.workflow_id)
        ? old.map((item) => (item.workflow_id === draft.workflow_id
          ? { ...draft, name: draft.name || item.name, description: draft.description ?? item.description }
          : item))
        : [...old, draft]
    ));
    setSelectedId(draft.workflow_id);
    setGraphState(draft.graph);
    setRevision(draft.revision);
    setDirty(false);
  }, []);

  const confirmLeave = useCallback(async () => {
    if (!dirty) return true;
    return confirmDialog({ message: t("task.workflowEditor.leaveConfirm"), danger: true });
  }, [dirty, t]);

  return { drafts, selected, selectedId, graph, revision, dirty, loading, saving, deletingId, error, metaBusy, setGraph, updateMeta, choose, reloadSelected, create, remove, save, revert, adoptDraft, confirmLeave };
}
