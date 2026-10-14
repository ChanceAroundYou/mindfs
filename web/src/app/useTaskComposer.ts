/**
 * 任务创建/编辑编排 —— T2 簇（~250 行）。
 *
 * 覆盖：创建/编辑状态初始化与清理、项目切换时偏好恢复、
 * candidate/attachment/upload progress、createTask payload 组装。
 *
 * 做法：hook，参数面 UseTaskComposerParams 即完整契约；函数体逐字搬入。
 *
 * TDZ：saveTaskInlineEdit 引用 refreshTaskWorktree（后置初始化），
 * 通过 refreshTaskWorktreeRef 传入，调用点用可选链。
 */
import { useCallback, useEffect, type MutableRefObject, type Dispatch, type SetStateAction, type ChangeEvent } from "react";
import { useI18n } from "../i18n";
import { fetchGitBranches, type GitBranchesPayload } from "../services/git";
import { createTask } from "../services/tasks";
import { uploadFiles, formatFileToken, fileTokenPath, isUploadAbortError } from "../services/upload";
import { reportError } from "../services/net/error";
import { firstUserInputTemplate, taskStagesForCreate, findTaskProjectRoot, parseTaskProjectScopeKey, taskProjectScopeKey, type TaskInlineEditState } from "./appTask";
import { loadTaskCreateWorktreePreference, saveTaskCreateWorktreePreference } from "./appStorage";
import type { CandidateItem } from "../services/candidates";
import type { TaskTemplate } from "../services/tasks";

export interface UseTaskComposerParams {
  currentRootId: string | null;
  currentRootIdRef: MutableRefObject<string | null>;
  currentRootNodeIdRef: MutableRefObject<string | null>;
  managedRootByIdRef: MutableRefObject<Record<string, import("./appMisc").ManagedRootPayload>>;
  managedRootByKeyRef: MutableRefObject<Record<string, import("./appMisc").ManagedRootPayload>>;
  taskInlineEdit: TaskInlineEditState | null;
  taskTemplates: TaskTemplate[];
  taskInlineCandidates: CandidateItem[];
  taskInlineCandidateIndex: number;
  taskInlineUploadAbortRef: MutableRefObject<AbortController | null>;
  taskInlineEditorRef: MutableRefObject<import("../components/editor/TokenEditor").TokenEditorHandle | null>;
  taskInlineCandidateItemRefs: MutableRefObject<Array<HTMLElement | null>>;
  refreshTaskWorktreeRef: MutableRefObject<((rootId: string, worktreePath: string, force?: boolean, targetNodeId?: string) => Promise<void>) | null>;
  taskWorktreeBranchRequestRef: MutableRefObject<number>;
  setTaskInlineEdit: Dispatch<SetStateAction<TaskInlineEditState | null>>;
  setTaskInlineActiveToken: Dispatch<SetStateAction<{ type: "file" | "slash" | "prompt" | "command"; query: string } | null>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineHelpKey: Dispatch<SetStateAction<string>>;
  setTaskInlineSaving: Dispatch<SetStateAction<boolean>>;
  setTaskInlineUploadProgress: Dispatch<SetStateAction<import("../services/upload").UploadProgress | null>>;
  setTaskWorktreeBranches: Dispatch<SetStateAction<GitBranchesPayload>>;
  setTaskWorktreeBranchesLoading: Dispatch<SetStateAction<boolean>>;
  setTaskWorktreeBranchError: Dispatch<SetStateAction<string>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  t: ReturnType<typeof useI18n>["t"];
  applyTaskDetails: (rootId: string, details: import("../services/tasks").TaskDetail[], persist?: boolean) => void;
}

export function useTaskComposer(params: UseTaskComposerParams) {
  const {
    currentRootId,
    currentRootIdRef,
    currentRootNodeIdRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    taskInlineEdit,
    taskTemplates,
    taskInlineCandidates,
    taskInlineCandidateIndex,
    taskInlineUploadAbortRef,
    taskInlineEditorRef,
    taskInlineCandidateItemRefs,
    refreshTaskWorktreeRef,
    taskWorktreeBranchRequestRef,
    setTaskInlineEdit,
    setTaskInlineActiveToken,
    setTaskInlineCandidates,
    setTaskInlineCandidateIndex,
    setTaskInlineHelpKey,
    setTaskInlineSaving,
    setTaskInlineUploadProgress,
    setTaskWorktreeBranches,
    setTaskWorktreeBranchesLoading,
    setTaskWorktreeBranchError,
    getNodeIdForRoot,
    t,
    applyTaskDetails,
  } = params;

  const loadTaskWorktreeBranches = useCallback(async (rootId: string, nodeId?: string) => {
    if (!rootId) return;
    const requestId = ++taskWorktreeBranchRequestRef.current;
    setTaskWorktreeBranchesLoading(true);
    setTaskWorktreeBranches({ branches: [] });
    setTaskWorktreeBranchError("");
    try {
      const branches = await fetchGitBranches(rootId, nodeId || getNodeIdForRoot(rootId));
      if (requestId === taskWorktreeBranchRequestRef.current) setTaskWorktreeBranches(branches);
    } catch (error) {
      if (requestId === taskWorktreeBranchRequestRef.current) {
        setTaskWorktreeBranches({ branches: [] });
        setTaskWorktreeBranchError(error instanceof Error ? error.message : t("worktree.loadBranchFailed"));
      }
    } finally {
      if (requestId === taskWorktreeBranchRequestRef.current) setTaskWorktreeBranchesLoading(false);
    }
  }, [getNodeIdForRoot, t]);

	  // 分支列表跟着面板的目标项目和节点走；跨节点同名项目不能共用 Git 元数据。
	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit) return;
	    const rootId = edit.targetRootId || currentRootId || "";
	    const nodeId = edit.targetNodeId || getNodeIdForRoot(rootId);
	    const root = findTaskProjectRoot(rootId, nodeId, managedRootByKeyRef.current, managedRootByIdRef.current);
	    if (!edit.createWorktree || !edit.canToggleWorktree || !rootId || root?.is_git_repo !== true) {
	      taskWorktreeBranchRequestRef.current += 1;
	      setTaskWorktreeBranchesLoading(false);
	      setTaskWorktreeBranches({ branches: [] });
	      setTaskWorktreeBranchError("");
	      return;
	    }
	    void loadTaskWorktreeBranches(rootId, nodeId);
	  }, [currentRootId, getNodeIdForRoot, loadTaskWorktreeBranches, taskInlineEdit?.canToggleWorktree, taskInlineEdit?.createWorktree, taskInlineEdit?.targetNodeId, taskInlineEdit?.targetRootId]);

	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit || !edit.canToggleWorktree) return;
	    const rootId = edit.targetRootId || currentRootIdRef.current || "";
	    if (!rootId) return;
	    const nodeId = edit.targetNodeId || getNodeIdForRoot(rootId);
	    saveTaskCreateWorktreePreference(rootId, {
	      createWorktree: edit.createWorktree,
	      worktreeBranchMode: edit.worktreeBranchMode,
	      worktreeBranch: edit.worktreeBranch,
	    }, nodeId);
	  }, [
	    getNodeIdForRoot,
	    taskInlineEdit?.canToggleWorktree,
	    taskInlineEdit?.createWorktree,
	    taskInlineEdit?.worktreeBranch,
	    taskInlineEdit?.worktreeBranchMode,
	    taskInlineEdit?.targetNodeId,
	    taskInlineEdit?.targetRootId,
	  ]);

	  const openTaskCreateDialog = useCallback((template: TaskTemplate | null, targetRootId?: string, targetNodeId?: string) => {

	    const templateId = template?.id || "";

	    if (!templateId) return;

	    const initialText = firstUserInputTemplate(template);

	    const rootId = targetRootId || currentRootIdRef.current || "";

	    if (!rootId) return;

	    const nodeId = targetNodeId || (rootId === currentRootIdRef.current ? currentRootNodeIdRef.current : "") || getNodeIdForRoot(rootId) || "";

	    const projectKey = taskProjectScopeKey(rootId, nodeId);

	    const root = findTaskProjectRoot(rootId, nodeId, managedRootByKeyRef.current, managedRootByIdRef.current);

	    const taskCanCreateWorktree = root?.is_git_repo === true;

	    const worktreePref = loadTaskCreateWorktreePreference(rootId, nodeId);

	    setTaskInlineEdit({

	      templateId,

	      templateName: template?.name || t("task.defaultTitle"),

	      text: initialText,

	      targetRootId: targetRootId || undefined,

	      targetNodeId: nodeId || undefined,

	      // 只有工作台来的才带项目下拉：那里没有「当前项目」这个默认落点。

	      allowProjectSwitch: Boolean(targetRootId),

	      createWorktreePerRoot: {

	        [projectKey]: {

	          createWorktree: taskCanCreateWorktree && worktreePref.createWorktree,

	          worktreeBranchMode: worktreePref.worktreeBranchMode,

	          worktreeBranch: worktreePref.worktreeBranch,

	        },

	      },

	      name: "",
	      // 首段的「立即执行」：面板开出来就照模板的面板值。
	      startImmediately: template?.stages?.[0]?.snapshot?.start_immediately === true,
	      previousInputs: [],
	      createWorktree: taskCanCreateWorktree && worktreePref.createWorktree,
	      worktreeBranchMode: worktreePref.worktreeBranchMode,
	      worktreeBranch: worktreePref.worktreeBranch,
	      canToggleWorktree: true,
	      attachments: [],
	    });
	    setTaskInlineActiveToken(null);
	    setTaskInlineCandidates([]);
	    setTaskInlineCandidateIndex(0);
	  }, [t]);

  // 面板上换项目：worktree 三个开关是**按项目存**的，换回去要还原回这个项目那份，
  // 第一次切到某项目则从 localStorage 的偏好起头。切项目不碰正文/模板/附件。
  const switchTaskInlineEditProject = useCallback((nextProjectKey: string) => {

    setTaskInlineEdit((prev) => {

      if (!prev || !nextProjectKey) return prev;

      const { rootId: nextRootId, nodeId: nextNodeId } = parseTaskProjectScopeKey(nextProjectKey);

      if (!nextRootId) return prev;

      const prevRootId = prev.targetRootId || currentRootIdRef.current || "";

      const prevNodeId = prev.targetNodeId || getNodeIdForRoot(prevRootId) || "";

      const prevProjectKey = taskProjectScopeKey(prevRootId, prevNodeId);

      const nextScopedProjectKey = taskProjectScopeKey(nextRootId, nextNodeId);

      if (nextScopedProjectKey === prevProjectKey) return prev;

      const stash = (state: TaskInlineEditState) => ({

        createWorktree: state.createWorktree,

        worktreeBranchMode: state.worktreeBranchMode,

        worktreeBranch: state.worktreeBranch,

      });

      const perRoot = { ...(prev.createWorktreePerRoot || {}), [prevProjectKey]: stash(prev) };

      const restored = perRoot[nextScopedProjectKey];

      const root = findTaskProjectRoot(nextRootId, nextNodeId, managedRootByKeyRef.current, managedRootByIdRef.current);

      const canCreate = root?.is_git_repo === true;

      const stored = restored || loadTaskCreateWorktreePreference(nextRootId, nextNodeId);

      const nextPref = { ...stored, createWorktree: canCreate && stored.createWorktree };

      return {

        ...prev,

        targetRootId: nextRootId,

        targetNodeId: nextNodeId || undefined,

        createWorktreePerRoot: { ...perRoot, [nextScopedProjectKey]: nextPref },

        createWorktree: nextPref.createWorktree,

        worktreeBranchMode: nextPref.worktreeBranchMode,

        worktreeBranch: nextPref.worktreeBranch,

        canToggleWorktree: true,

      };

    });

  }, [getNodeIdForRoot]);


  const closeTaskEditDialog = useCallback(() => {
    setTaskInlineEdit((prev) => {
      prev?.attachments.forEach((attachment) => {
        if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
      });
      return null;
    });
    setTaskInlineActiveToken(null);
    setTaskInlineCandidates([]);
    setTaskInlineCandidateIndex(0);
    setTaskInlineHelpKey("");
    setTaskInlineSaving(false);
    setTaskInlineUploadProgress(null);
    taskInlineUploadAbortRef.current?.abort();
    taskInlineUploadAbortRef.current = null;
  }, []);

  const applyTaskInlineCandidate = useCallback((candidate: CandidateItem) => {
    setTaskInlineCandidates([]);
    setTaskInlineCandidateIndex(0);
    taskInlineEditorRef.current?.insertCandidate(candidate.type, candidate.name);
    taskInlineEditorRef.current?.focus();
  }, []);

  useEffect(() => {
    if (taskInlineCandidates.length === 0) {
      taskInlineCandidateItemRefs.current = [];
      return;
    }
    const activeItem = taskInlineCandidateItemRefs.current[taskInlineCandidateIndex];
    activeItem?.scrollIntoView({ block: "nearest" });
  }, [taskInlineCandidates, taskInlineCandidateIndex]);

  const appendTaskInlineAttachments = useCallback((files: File[]) => {
    if (files.length === 0) return;
    setTaskInlineEdit((prev) => {
      if (!prev) return prev;
      return {
        ...prev,
        attachments: [
          ...prev.attachments,
          ...files.map((file) => ({
            id: `${file.name}-${file.size}-${file.lastModified}-${Math.random().toString(36).slice(2, 8)}`,
            file,
            isImage: file.type.startsWith("image/"),
            previewUrl: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined,
          })),
        ],
      };
    });
  }, []);

  const handleTaskInlineAttachmentChange = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    if (files.length > 0) {
      appendTaskInlineAttachments(files);
    }
    event.currentTarget.value = "";
  }, [appendTaskInlineAttachments]);


  const removeTaskInlineAttachment = useCallback((id: string) => {
    setTaskInlineEdit((prev) => prev
      ? {
          ...prev,
          attachments: prev.attachments.filter((attachment) => {
            const keep = attachment.id !== id;
            if (!keep && attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
            return keep;
          }),
        }
      : prev);
  }, []);

  const saveTaskInlineEdit = useCallback(async (prompt: string) => {

    const edit = taskInlineEdit;

    const rootId = edit?.targetRootId || currentRootIdRef.current;

    if (!edit || !rootId) return;

    setTaskInlineSaving(true);

    setTaskInlineUploadProgress(null);

    try {

      const nodeId = edit.targetNodeId || getNodeIdForRoot(rootId);

      let attachmentTokens = "";

      if (edit.attachments.length > 0) {

        const uploadAbort = new AbortController();

        taskInlineUploadAbortRef.current = uploadAbort;

        const uploaded = await uploadFiles({

          rootId,

          files: edit.attachments.map((attachment) => attachment.file),

          onProgress: setTaskInlineUploadProgress,

          signal: uploadAbort.signal,

          nodeId,

        });

        attachmentTokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");

      }

      const payload = [prompt.trim(), attachmentTokens].filter(Boolean).join("\n");

      const root = findTaskProjectRoot(rootId, nodeId, managedRootByKeyRef.current, managedRootByIdRef.current);

      const taskCanCreateWorktree = root?.is_git_repo === true;

      const createWorktree = taskCanCreateWorktree && edit.createWorktree;

      // 选了模板就把流水随包带上（taskStagesForCreate：有 agent/模型/立即执行覆盖就用覆盖后的，

      // 没有就用模板自带的）。不能像以前那样「没覆盖就不带」让后端回查 ——

      // 任务可能建在没有模板库的 worker 节点上，那次回查必然失败（service.go CreateTask）。

      const selectedTemplate = taskTemplates.find((tpl) => tpl.id === edit.templateId) || null;

      const createStages = taskStagesForCreate(

        selectedTemplate,

        { agent: edit.agentOverride, model: edit.modelOverride, effort: edit.effortOverride, startImmediately: edit.startImmediately },

      );

      const detail = await createTask(

        rootId,

        edit.templateId,

        payload,

        createWorktree,

        edit.worktreeBranchMode,

        edit.worktreeBranch,

        nodeId,

        {

          ...(edit.name?.trim() ? { name: edit.name.trim() } : {}),

          ...(createStages ? { stages: createStages } : {}),

          // 模板名一并带上：worker 节点查不到模板库，看板上的「模板来源」标签靠它。

          ...(selectedTemplate?.name ? { templateName: selectedTemplate.name } : {}),

        },

      );

      applyTaskDetails(rootId, [detail]);

      if (detail.task.worktree_path) {

        void refreshTaskWorktreeRef.current?.(rootId, detail.task.worktree_path, true, nodeId);

      }

      closeTaskEditDialog();

    } catch (err) {

      if (!isUploadAbortError(err)) {

        reportError("file.write_failed", String((err as Error)?.message || t("task.saveFailed")));

      }

      setTaskInlineSaving(false);

      setTaskInlineUploadProgress(null);

      taskInlineUploadAbortRef.current = null;

    }

  }, [applyTaskDetails, closeTaskEditDialog, getNodeIdForRoot, taskInlineEdit, taskTemplates, t, refreshTaskWorktreeRef]);


  return {
    loadTaskWorktreeBranches,
    openTaskCreateDialog,
    switchTaskInlineEditProject,
    closeTaskEditDialog,
    applyTaskInlineCandidate,
    appendTaskInlineAttachments,
    handleTaskInlineAttachmentChange,
    removeTaskInlineAttachment,
    saveTaskInlineEdit,
  };
}
