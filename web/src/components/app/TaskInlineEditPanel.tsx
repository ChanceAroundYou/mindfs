// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { PanelShell } from "../shell/PanelShell";
import { composerInputStyle } from "../action/composerStyles";
import { findTaskProjectRoot, taskProjectScopeKey } from "../../app/appTask";
import { CompactUploadProgress } from "../file/CompactUploadProgress";
import { WorktreeBranchSelector } from "../task/WorktreeBranchSelector";
import { NoWorktreeIcon } from "../common/NoWorktreeIcon";
import { PromptEditor } from "../editor/PromptEditor";
import { Select } from "../common/Select";
import { useI18n } from "../../i18n";
import type { Dispatch, SetStateAction, RefObject, ChangeEvent } from "react";
import React, { useEffect } from "react";
import type { TokenEditorHandle } from "../editor/TokenEditor";
import type { CandidateItem } from "../../shared/candidateTypes";
import type { GitBranchesPayload } from "../../shared/gitTypes";
import type { UploadProgress } from "../../shared/uploadTypes";
import { ManagedRootPayload } from "../../app/appMisc";
import { TaskInlineEditState } from "../../app/appTask";
import { TaskInlineCandidateMenu } from "./TaskInlineCandidateMenu";

export interface TaskInlineEditPanelProps {
  PanelShell: any;
  applyTaskInlineCandidate: (candidate: CandidateItem) => void;
  closeTaskEditDialog: () => void;
  composerInputStyle: any;
  currentRootId: string | null;
  handleTaskInlineAttachmentChange: (event: ChangeEvent<HTMLInputElement, React.JSX.Element>) => void;
  isMobile: boolean;
  managedRootByKeyRef: RefObject<Record<string, ManagedRootPayload>>;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  removeTaskInlineAttachment: (id: string) => void;
  saveTaskInlineEdit: (text: string) => Promise<void>;
  setTaskInlineActiveToken: Dispatch<SetStateAction<{ type: "file" | "slash" | "prompt" | "command"; query: string; } | null>>;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
  setTaskInlineEdit: Dispatch<SetStateAction<TaskInlineEditState | null>>;
  switchTaskInlineEditProject: (nextRootId: string) => void;
  t: ReturnType<typeof useI18n>["t"];
  taskCreateProjectOptions: { value: string; label: string; color: string | undefined; }[];
  taskInlineActiveToken: { type: "file" | "slash" | "prompt" | "command"; query: string; } | null;
  taskInlineAttachmentInputRef: RefObject<HTMLInputElement | null>;
  taskInlineCandidateIndex: number;
  taskInlineCandidateItemRefs: RefObject<(HTMLButtonElement | null)[]>;
  taskInlineCandidates: CandidateItem[];
  taskInlineEdit: TaskInlineEditState | null;
  taskInlineEditorRef: RefObject<TokenEditorHandle | null>;
  taskInlineSaving: boolean;
  taskInlineUploadAbortRef: RefObject<AbortController | null>;
  taskInlineUploadProgress: UploadProgress | null;
  taskWorktreeBranches: GitBranchesPayload;
}

/**
 * 新建任务面板 = 一条留言 + 可选的 worktree 配置 + 「立即执行」开关。
 *
 * 不再有阶段编辑：任务的第一轮由后端 router 决定走直连回话还是某个 workflow，
 * 面板上没有 agent/model/effort 可选。唯一的机械开关是「立即执行」（默认勾上）：
 * 勾掉时首条留言先排队，任务停在「待开始」，之后在对话流里点「立即执行」再开跑。
 */
export function TaskInlineEditPanel(props: TaskInlineEditPanelProps) {
  const PanelShell = props.PanelShell;
  const applyTaskInlineCandidate = props.applyTaskInlineCandidate;
  const closeTaskEditDialog = props.closeTaskEditDialog;
  const composerInputStyle = props.composerInputStyle;
  const currentRootId = props.currentRootId;
  const handleTaskInlineAttachmentChange = props.handleTaskInlineAttachmentChange;
  const isMobile = props.isMobile;
  const managedRootByKeyRef = props.managedRootByKeyRef;
  const managedRootByIdRef = props.managedRootByIdRef;
  const getNodeIdForRoot = props.getNodeIdForRoot;
  const removeTaskInlineAttachment = props.removeTaskInlineAttachment;
  const saveTaskInlineEdit = props.saveTaskInlineEdit;
  const setTaskInlineActiveToken = props.setTaskInlineActiveToken;
  const setTaskInlineCandidateIndex = props.setTaskInlineCandidateIndex;
  const setTaskInlineCandidates = props.setTaskInlineCandidates;
  const setTaskInlineEdit = props.setTaskInlineEdit;
  const switchTaskInlineEditProject = props.switchTaskInlineEditProject;
  const t = props.t;
  const taskCreateProjectOptions = props.taskCreateProjectOptions;
  const taskInlineActiveToken = props.taskInlineActiveToken;
  const taskInlineAttachmentInputRef = props.taskInlineAttachmentInputRef;
  const taskInlineCandidateIndex = props.taskInlineCandidateIndex;
  const taskInlineCandidateItemRefs = props.taskInlineCandidateItemRefs;
  const taskInlineCandidates = props.taskInlineCandidates;
  const taskInlineEdit = props.taskInlineEdit;
  const taskInlineEditorRef = props.taskInlineEditorRef;
  const taskInlineSaving = props.taskInlineSaving;
  const taskInlineUploadAbortRef = props.taskInlineUploadAbortRef;
  const taskInlineUploadProgress = props.taskInlineUploadProgress;
  const taskWorktreeBranches = props.taskWorktreeBranches;

  // 打开面板时把光标放进编辑区：TokenEditor 不会自动聚焦，以前这一步挂在
  // 任务模板状态 hook 上（按 templateId 触发），模板下线后挪到这里，用
  // 「是否打开 + 目标项目」当重置键。
  const taskInlineOpen = taskInlineEdit !== null;
  const taskInlineResetKey = `${taskInlineEdit?.targetRootId || ""}|${taskInlineEdit?.targetNodeId || ""}`;
  useEffect(() => {
    if (!taskInlineEdit) return;
    const timer = window.setTimeout(() => {
      taskInlineEditorRef.current?.setText(taskInlineEdit.text || "");
      taskInlineEditorRef.current?.focus();
    }, 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskInlineOpen, taskInlineResetKey]);

  return (
    <>
    {taskInlineEdit ? (
    	        (() => {
    	          // worktree 开关跟的是**面板的目标项目**：从工作台发起时那不是当前项目，
    	          // 写死 currentRootId 会让「非 git 项目也显示 worktree 开关」。
    	          const taskInlineTargetRootId = taskInlineEdit.targetRootId || currentRootId || "";
    	          const taskInlineTargetNodeId = taskInlineEdit.targetNodeId || getNodeIdForRoot(taskInlineTargetRootId) || "";
    	          const taskInlineRoot = findTaskProjectRoot(taskInlineTargetRootId, taskInlineTargetNodeId, managedRootByKeyRef.current, managedRootByIdRef.current);
    	          const taskInlineCanCreateWorktree = taskInlineRoot?.is_git_repo === true;
    	          const showTaskWorktreeControls = taskInlineCanCreateWorktree && taskInlineEdit.canToggleWorktree;
    	          const taskWorktreeControlsEditable = taskInlineEdit.canToggleWorktree;
    	          return (
            <PanelShell
              width={640}
              onClose={() => { if (!taskInlineSaving) closeTaskEditDialog(); }}
              hasUnsavedChanges={() => String(taskInlineEdit?.text || "").trim() !== ""}
              title={(
                <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "8px", minWidth: 0 }}>
                  <div style={{ fontSize: "13px", fontWeight: 800, color: "var(--text-color)", whiteSpace: "nowrap" }}>
                    {t("task.createDialogTitle")}
                  </div>
                  {taskInlineEdit.allowProjectSwitch && taskCreateProjectOptions.length > 1 ? (
                    <div style={{ minWidth: "120px", maxWidth: "160px" }}>
                      <Select
                        value={taskProjectScopeKey(taskInlineTargetRootId, taskInlineTargetNodeId)}
                        ariaLabel={t("task.workspaceSelectProject")}
                        onChange={(value) => switchTaskInlineEditProject(value)}
                        options={taskCreateProjectOptions}
                        size="panel"
                      />
                    </div>
                  ) : null}
                  {showTaskWorktreeControls ? (
                    <button
                      type="button"
                      onClick={() => {
                        if (!taskWorktreeControlsEditable) return;
                        setTaskInlineEdit((prev) => prev ? { ...prev, createWorktree: !prev.createWorktree } : prev);
                      }}
                      disabled={taskInlineSaving || !taskWorktreeControlsEditable}
                      aria-label={taskInlineEdit.createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
                      title={taskInlineEdit.createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
                      style={{
                        height: "26px",
                        borderRadius: "6px",
                        border: taskInlineEdit.createWorktree ? "1px solid rgba(22, 163, 74, 0.28)" : "1px solid var(--border-color)",
                        background: taskInlineEdit.createWorktree ? "rgba(22, 163, 74, 0.08)" : "rgba(100, 116, 139, 0.10)",
                        color: taskInlineEdit.createWorktree ? "#15803d" : "var(--text-secondary)",
                        padding: "0 8px",
                        fontSize: "12px",
                        fontWeight: 700,
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        gap: "3px",
                        flexShrink: 0,
                      }}
                    >
                      {taskInlineEdit.createWorktree ? "worktree" : (
                        <>
                          <NoWorktreeIcon size={12} />
                          worktree
                        </>
                      )}
                    </button>
                  ) : null}
                  {showTaskWorktreeControls && taskInlineEdit.createWorktree ? (
                    <div style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0 }}>
                      <WorktreeBranchSelector
                        branchMode={taskInlineEdit.worktreeBranchMode}
                        branch={taskInlineEdit.worktreeBranch}
                        branches={taskWorktreeBranches.branches}
                        disabled={taskInlineSaving || !taskWorktreeControlsEditable}
                        height={26}
                        maxWidth={isMobile ? 160 : 240}
                        menuAlign={isMobile ? "left" : "right"}
                        menuPlacement="bottom"
                        onChange={(nextMode, nextBranch) => {
                          setTaskInlineEdit((prev) => {
                            if (!prev) return prev;
                            return { ...prev, worktreeBranchMode: nextMode, worktreeBranch: nextBranch };
                          });
                        }}
                      />
                      {/* 新分支名跟分支选择器同一行：以前它是个 margin 撑出来的裸
                          input，悬在正文容器外面，与任务名输入框完全不是一套。 */}
                      {taskInlineEdit.worktreeBranchMode === "new" ? (
                        <input
                          aria-label={t("task.newBranchName")}
                          placeholder={t("task.newBranchName")}
                          value={taskInlineEdit.worktreeBranch}
                          disabled={taskInlineSaving || !taskWorktreeControlsEditable}
                          onChange={(event) => {
                            const value = event.target.value;
                            setTaskInlineEdit((prev) => (prev ? { ...prev, worktreeBranch: value } : prev));
                          }}
                          style={{ ...composerInputStyle, height: "26px", width: isMobile ? "120px" : "160px", flex: "0 1 auto" }}
                        />
                      ) : null}
                    </div>
                  ) : null}
                </div>
              )}
            >
                <div style={{ padding: "12px", overflow: "visible", position: "relative", minHeight: 0, display: "flex", flexDirection: "column" }}>
                  <TaskInlineCandidateMenu
                    visible={!!taskInlineActiveToken}
                    isMobile={isMobile}
                    candidates={taskInlineCandidates}
                    selectedIndex={taskInlineCandidateIndex}
                    itemRefs={taskInlineCandidateItemRefs}
                    onSelect={applyTaskInlineCandidate}
                    onSelectedIndexChange={setTaskInlineCandidateIndex}
                  />
                  <div style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap", marginBottom: "8px" }}>
                    <input
                      value={taskInlineEdit.name}
                      onChange={(event) => setTaskInlineEdit((prev) => prev ? { ...prev, name: event.target.value } : prev)}
                      placeholder={t("task.namePlaceholder")}
                      style={{ ...composerInputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontWeight: 800 }}
                    />
                    {/* 「立即执行」默认勾上：勾掉时任务只落卡，首条留言排队等后
                        面的「立即执行」。与 worktree 的标题行开关分开摆，是因为它
                        属于「创建后怎么跑」，不是项目/树配置。 */}
                    <label
                      style={{
                        marginLeft: "auto",
                        height: "26px",
                        display: "inline-flex",
                        alignItems: "center",
                        gap: "5px",
                        padding: "0 8px",
                        borderRadius: "6px",
                        border: `1px solid ${taskInlineEdit.startImmediately ? "var(--accent-color)" : "var(--border-color)"}`,
                        background: taskInlineEdit.startImmediately ? "rgba(37, 99, 235, 0.10)" : "transparent",
                        color: taskInlineEdit.startImmediately ? "var(--accent-color)" : "var(--text-secondary)",
                        fontSize: "12px",
                        fontWeight: 700,
                        cursor: taskInlineSaving ? "not-allowed" : "pointer",
                        userSelect: "none",
                        whiteSpace: "nowrap",
                        opacity: taskInlineSaving ? 0.6 : 1,
                      }}
                    >
                      <input
                        type="checkbox"
                        checked={taskInlineEdit.startImmediately}
                        disabled={taskInlineSaving}
                        onChange={(event) => {
                          const checked = event.target.checked;
                          setTaskInlineEdit((prev) => prev ? { ...prev, startImmediately: checked } : prev);
                        }}
                        style={{ accentColor: "var(--accent-color)", cursor: taskInlineSaving ? "not-allowed" : "pointer", margin: 0 }}
                      />
                      {t("task.startImmediately")}
                    </label>
                  </div>
                  <PromptEditor
                    ref={taskInlineEditorRef}
                    value={taskInlineEdit.text}
                    onChange={(next) => setTaskInlineEdit((prev) => prev ? { ...prev, text: next } : prev)}
                    onAttach={() => taskInlineAttachmentInputRef.current?.click()}
                    onSend={(text: string) => void saveTaskInlineEdit(text)}
                    sending={taskInlineSaving}
                    sendDisabled={!taskInlineEdit.text.trim()}
                    placeholder={t("task.editPlaceholder")}
                  />
                  {taskInlineEdit.attachments.length > 0 || taskInlineUploadProgress ? (
                    <div style={{ marginTop: "10px", display: "flex", flexWrap: "wrap", gap: "8px" }}>
                      {taskInlineEdit.attachments.map((attachment) => attachment.isImage && attachment.previewUrl ? (
                        <div
                          key={attachment.id}
                          style={{
                            width: "54px",
                            height: "54px",
                            borderRadius: "8px",
                            border: "1px solid var(--border-color)",
                            background: "rgba(100, 116, 139, 0.10)",
                            position: "relative",
                            overflow: "hidden",
                          }}
                        >
                          <img src={attachment.previewUrl} alt={attachment.file.name} style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }} />
                          <button
                            type="button"
                            onClick={() => removeTaskInlineAttachment(attachment.id)}
                            disabled={taskInlineSaving}
                            aria-label={t("task.removeAttachment", { name: attachment.file.name })}
                            style={{
                              position: "absolute",
                              top: "2px",
                              right: "2px",
                              width: "18px",
                              height: "18px",
                              border: "none",
                              borderRadius: "999px",
                              background: "rgba(15, 23, 42, 0.72)",
                              color: "#fff",
                              cursor: taskInlineSaving ? "not-allowed" : "pointer",
                              padding: 0,
                              lineHeight: "18px",
                            }}
                          >
                            ×
                          </button>
                        </div>
                      ) : (
                        <span
                          key={attachment.id}
                          style={{
                            maxWidth: "100%",
                            border: "1px solid var(--border-color)",
                            borderRadius: "999px",
                            background: "rgba(100, 116, 139, 0.10)",
                            color: "var(--text-color)",
                            display: "inline-flex",
                            alignItems: "center",
                            gap: "6px",
                            padding: "4px 7px",
                            fontSize: "12px",
                          }}
                        >
                          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{attachment.file.name}</span>
                          <button
                            type="button"
                            onClick={() => removeTaskInlineAttachment(attachment.id)}
                            disabled={taskInlineSaving}
                            style={{
                              border: "none",
                              background: "transparent",
                              color: "var(--text-secondary)",
                              cursor: taskInlineSaving ? "not-allowed" : "pointer",
                              padding: 0,
                            }}
                          >
                            ×
                          </button>
                        </span>
                      ))}
                      <CompactUploadProgress
                        progress={taskInlineUploadProgress}
                        label={t("upload.attachmentsProgress")}
                        statusLabel={t("upload.inProgress")}
                        cancelLabel={t("upload.cancel")}
                        onCancel={() => taskInlineUploadAbortRef.current?.abort()}
                      />
                    </div>
                  ) : null}
                  <input
                    ref={taskInlineAttachmentInputRef}
                    type="file"
                    multiple
                    style={{ display: "none" }}
                    onChange={handleTaskInlineAttachmentChange}
                  />
                </div>
        </PanelShell>
              );
            })()
          ) : null}
    </>
  );
}
