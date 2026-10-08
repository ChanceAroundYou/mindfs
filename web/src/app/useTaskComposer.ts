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
import { firstUserInputTemplate, taskStagesForCreate, type TaskInlineEditState } from "./appTask";
import { loadTaskCreateWorktreePreference, saveTaskCreateWorktreePreference } from "./appStorage";
import type { CandidateItem } from "../services/candidates";
import type { TaskTemplate } from "../services/tasks";

export interface UseTaskComposerParams {
  currentRootId: string | null;
  currentRootIdRef: MutableRefObject<string | null>;
  managedRootByIdRef: MutableRefObject<Record<string, import("./appMisc").ManagedRootPayload>>;
  taskInlineEdit: TaskInlineEditState | null;
  taskTemplates: TaskTemplate[];
  taskInlineCandidates: CandidateItem[];
  taskInlineCandidateIndex: number;
  taskInlineUploadAbortRef: MutableRefObject<AbortController | null>;
  taskInlineEditorRef: MutableRefObject<import("../components/editor/TokenEditor").TokenEditorHandle | null>;
  taskInlineCandidateItemRefs: MutableRefObject<Array<HTMLElement | null>>;
  refreshTaskWorktreeRef: MutableRefObject<((rootId: string, worktreePath: string) => Promise<void>) | null>;
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
    managedRootByIdRef,
    taskInlineEdit,
    taskTemplates,
    taskInlineCandidates,
    taskInlineCandidateIndex,
    taskInlineUploadAbortRef,
    taskInlineEditorRef,
    taskInlineCandidateItemRefs,
    refreshTaskWorktreeRef,
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

  const loadTaskWorktreeBranches = useCallback(async (rootId: string) => {
    if (!rootId) return;
    setTaskWorktreeBranchesLoading(true);
    setTaskWorktreeBranchError("");
    try {
      setTaskWorktreeBranches(await fetchGitBranches(rootId, getNodeIdForRoot(rootId)));
    } catch (error) {
      setTaskWorktreeBranches({ branches: [] });
      setTaskWorktreeBranchError(error instanceof Error ? error.message : t("worktree.loadBranchFailed"));
    } finally {
      setTaskWorktreeBranchesLoading(false);
    }
  }, [t]);

	  // 分支列表跟的是**面板的目标项目**：从工作台发起时那不是 currentRootId，
	  // 照 currentRootId 拉会把别的项目的分支带进分支选择器。
	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit) return;
	    const rootId = edit.targetRootId || currentRootId || "";
	    if (!edit.createWorktree || !edit.canToggleWorktree || !rootId || managedRootByIdRef.current[rootId]?.is_git_repo !== true) {
	      setTaskWorktreeBranchError("");
	      return;
	    }
	    void loadTaskWorktreeBranches(rootId);
	  }, [currentRootId, loadTaskWorktreeBranches, taskInlineEdit?.canToggleWorktree, taskInlineEdit?.createWorktree, taskInlineEdit?.targetRootId]);

	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit || !edit.canToggleWorktree) return;
	    // 同样按目标项目存：从工作台发起时把 A 项目的偏好写到 B 项目上是错的。
	    const rootId = edit.targetRootId || currentRootIdRef.current || "";
	    if (!rootId) return;
	    saveTaskCreateWorktreePreference(rootId, {
	      createWorktree: edit.createWorktree,
	      worktreeBranchMode: edit.worktreeBranchMode,
      worktreeBranch: edit.worktreeBranch,
	    });
	  }, [
	    taskInlineEdit?.canToggleWorktree,
	    taskInlineEdit?.createWorktree,
	    taskInlineEdit?.worktreeBranch,
	    taskInlineEdit?.worktreeBranchMode,
	    taskInlineEdit?.targetRootId,
	  ]);

	  const openTaskCreateDialog = useCallback((template: TaskTemplate | null, targetRootId?: string) => {
	    const templateId = template?.id || "";
	    if (!templateId) return;
	    const initialText = firstUserInputTemplate(template);
	    // 工作台发起时可以指定项目；看板入口不传，落在当前项目上。
	    const rootId = targetRootId || currentRootIdRef.current || "";
	    const taskCanCreateWorktree = managedRootByIdRef.current[rootId]?.is_git_repo === true;
	    const worktreePref = loadTaskCreateWorktreePreference(rootId);
	    setTaskInlineEdit({
	      templateId,
	      templateName: template?.name || t("task.defaultTitle"),
	      text: initialText,
	      targetRootId: targetRootId || undefined,
	      // 只有工作台来的才带项目下拉：那里没有「当前项目」这个默认落点。
	      allowProjectSwitch: Boolean(targetRootId),
	      createWorktreePerRoot: {
	        [rootId]: {
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
  const switchTaskInlineEditProject = useCallback((nextRootId: string) => {
    setTaskInlineEdit((prev) => {
      if (!prev || !nextRootId || nextRootId === (prev.targetRootId || currentRootIdRef.current || "")) return prev;
      const prevRootId = prev.targetRootId || currentRootIdRef.current || "";
      const stash = (state: TaskInlineEditState, rootId: string) => ({
        createWorktree: state.createWorktree,
        worktreeBranchMode: state.worktreeBranchMode,
        worktreeBranch: state.worktreeBranch,
      });
      const perRoot = {
        ...(prev.createWorktreePerRoot || {}),
        [prevRootId]: stash(prev, prevRootId),
      };
      const restored = perRoot[nextRootId];
      const canCreate = managedRootByIdRef.current[nextRootId]?.is_git_repo === true;
      const pref = restored || (() => {
        const stored = loadTaskCreateWorktreePreference(nextRootId);
        return { createWorktree: canCreate && stored.createWorktree, worktreeBranchMode: stored.worktreeBranchMode, worktreeBranch: stored.worktreeBranch };
      })();
      return {
        ...prev,
        targetRootId: nextRootId,
        createWorktreePerRoot: { ...perRoot, [nextRootId]: pref },
        createWorktree: pref.createWorktree,
        worktreeBranchMode: pref.worktreeBranchMode,
        worktreeBranch: pref.worktreeBranch,
        canToggleWorktree: true,
      };
    });
  }, []);

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

  const saveTaskInlineEdit = useCallback(async () => {
    const edit = taskInlineEdit;
    const rootId = edit?.targetRootId || currentRootIdRef.current;
    if (!edit || !rootId) return;
    setTaskInlineSaving(true);
    setTaskInlineUploadProgress(null);
    try {
      let attachmentTokens = "";
      if (edit.attachments.length > 0) {
        const uploadAbort = new AbortController();
        taskInlineUploadAbortRef.current = uploadAbort;
        const uploaded = await uploadFiles({
          rootId,
          files: edit.attachments.map((attachment) => attachment.file),
          onProgress: setTaskInlineUploadProgress,
          signal: uploadAbort.signal,
          nodeId: getNodeIdForRoot(rootId),
        });
        attachmentTokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");
      }
      const payload = [edit.text.trim(), attachmentTokens].filter(Boolean).join("\n");
      const taskCanCreateWorktree = managedRootByIdRef.current[rootId]?.is_git_repo === true;
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
        getNodeIdForRoot(rootId),
        {
          ...(edit.name?.trim() ? { name: edit.name.trim() } : {}),
          ...(createStages ? { stages: createStages } : {}),
          // 模板名一并带上：worker 节点查不到模板库，看板上的「模板来源」标签靠它。
          ...(selectedTemplate?.name ? { templateName: selectedTemplate.name } : {}),
        },
      );
      applyTaskDetails(rootId, [detail]);
      if (detail.task.worktree_path) {
        void refreshTaskWorktreeRef.current?.(rootId, detail.task.worktree_path);
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
  }, [applyTaskDetails, closeTaskEditDialog, taskInlineEdit, taskTemplates, t, getNodeIdForRoot, refreshTaskWorktreeRef]);

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
