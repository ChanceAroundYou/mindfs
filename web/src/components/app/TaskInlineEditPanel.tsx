// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { PanelShell } from "../shell/PanelShell";
import { composerInputStyle } from "../action/composerStyles";
import { applyStageOverride, currentTaskInputFromDetail, DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, firstAgentStage, firstTaskInputFromDetail, firstUserInputTemplate, isTerminalKanbanTask, latestTaskStageRun, normalizeFastService, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, previousTaskInputsFromDetail, taskSessionKeysFromDetail, taskStagesForCreate, taskStatusLabel } from "../../app/appTask";
import { InlineTokenText } from "../common/InlineTokenText";
import { CompactUploadProgress } from "../file/CompactUploadProgress";
import { TaskTemplateDialog, FieldLabelWithInfo } from "../task/TaskTemplateDialog";
import { WorktreeBranchSelector } from "../task/WorktreeBranchSelector";
import { NoWorktreeIcon } from "../common/NoWorktreeIcon";
import { StageEditor } from "../task/StageEditor";
import { Select } from "../common/Select";
import { useI18n } from "../../i18n";
import type { Dispatch, SetStateAction, RefObject, ChangeEvent } from "react";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  buildGitDiffCacheSignature,
  clearGitHistoryCache,
  fetchGitDiff,
  fetchGitRelatedFileDiff,
  fetchGitBranches,
  removeGitWorktree,
  type GitBranchesPayload,
  type GitDiffPayload,
} from "../../services/git";
import { fileTokenPath, formatFileToken, isUploadAbortError, uploadFiles, type UploadProgress } from "../../services/upload";
import TokenEditor, { type TokenEditorHandle } from "../editor/TokenEditor";
import { fetchAgents, restartAgent, type AgentStatus } from "../../services/agents";
import { fetchCandidates, type CandidateItem } from "../../services/candidates";
import {
  beginTaskFinishWorktree,
  createTask,
  deleteTask,
  deleteTaskTemplate,
  // FinishWorktreeConflict 是 class 不是 type：下面要用 instanceof 分流
  // （冲突要列文件清单，其它失败只报一句）。
  FinishWorktreeConflict,
  fetchTaskDetails,
  fetchTaskTemplates,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
  rebuildTaskWorktree,
  saveTaskTemplate,
  upsertCachedTaskDetails,
  type KanbanTask,
  type StageRun,
  type TaskDetail,
  type TaskOverviewItem,
  type StageTemplate,
  type TaskTemplate,
} from "../../services/tasks";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "../../app/appMisc";
import { TaskInlineEditState } from "../../app/appTask";

export interface TaskInlineEditPanelProps {
  DEFAULT_TASK_AGENT: any;
  DEFAULT_TASK_MODEL: any;
  PanelShell: any;
  applyTaskInlineCandidate: (candidate: CandidateItem) => void;
  availableAgents: AgentStatus[];
  closeTaskEditDialog: () => void;
  composerInputStyle: any;
  currentRootId: string | null;
  firstAgentStage: any;
  firstUserInputTemplate: any;
  handleTaskInlineAttachmentChange: (event: ChangeEvent<HTMLInputElement, React.JSX.Element>) => void;
  isMobile: boolean;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  removeTaskInlineAttachment: (id: string) => void;
  saveTaskInlineEdit: () => Promise<void>;
  setTaskInlineActiveToken: Dispatch<SetStateAction<{ type: "file" | "slash" | "prompt" | "command"; query: string; } | null>>;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
  setTaskInlineEdit: Dispatch<SetStateAction<TaskInlineEditState | null>>;
  setTaskInlineHelpKey: Dispatch<SetStateAction<string>>;
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
  taskInlineHelpKey: string;
  taskInlineSaving: boolean;
  taskInlineUploadAbortRef: RefObject<AbortController | null>;
  taskInlineUploadProgress: UploadProgress | null;
  taskTemplates: TaskTemplate[];
  taskWorktreeBranches: GitBranchesPayload;
}

export function TaskInlineEditPanel(props: TaskInlineEditPanelProps) {
  const DEFAULT_TASK_AGENT = props.DEFAULT_TASK_AGENT;
  const DEFAULT_TASK_MODEL = props.DEFAULT_TASK_MODEL;
  const PanelShell = props.PanelShell;
  const applyTaskInlineCandidate = props.applyTaskInlineCandidate;
  const availableAgents = props.availableAgents;
  const closeTaskEditDialog = props.closeTaskEditDialog;
  const composerInputStyle = props.composerInputStyle;
  const currentRootId = props.currentRootId;
  const firstAgentStage = props.firstAgentStage;
  const firstUserInputTemplate = props.firstUserInputTemplate;
  const handleTaskInlineAttachmentChange = props.handleTaskInlineAttachmentChange;
  const isMobile = props.isMobile;
  const managedRootByIdRef = props.managedRootByIdRef;
  const removeTaskInlineAttachment = props.removeTaskInlineAttachment;
  const saveTaskInlineEdit = props.saveTaskInlineEdit;
  const setTaskInlineActiveToken = props.setTaskInlineActiveToken;
  const setTaskInlineCandidateIndex = props.setTaskInlineCandidateIndex;
  const setTaskInlineCandidates = props.setTaskInlineCandidates;
  const setTaskInlineEdit = props.setTaskInlineEdit;
  const setTaskInlineHelpKey = props.setTaskInlineHelpKey;
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
  const taskInlineHelpKey = props.taskInlineHelpKey;
  const taskInlineSaving = props.taskInlineSaving;
  const taskInlineUploadAbortRef = props.taskInlineUploadAbortRef;
  const taskInlineUploadProgress = props.taskInlineUploadProgress;
  const taskTemplates = props.taskTemplates;
  const taskWorktreeBranches = props.taskWorktreeBranches;

  return (
    <>
    {taskInlineEdit ? (
    	        (() => {
    	          // worktree 开关跟的是**面板的目标项目**：从工作台发起时那不是当前项目，
    	          // 写死 currentRootId 会让「非 git 项目也显示 worktree 开关」。
    	          const taskInlineTargetRootId = taskInlineEdit.targetRootId || currentRootId || "";
    	          const taskInlineCanCreateWorktree = managedRootByIdRef.current[taskInlineTargetRootId]?.is_git_repo === true;
    	          const showTaskWorktreeControls = taskInlineCanCreateWorktree && taskInlineEdit.canToggleWorktree;
    	          const taskWorktreeControlsEditable = taskInlineEdit.canToggleWorktree;
    	          // 编辑区里的 agent 选择器：默认值就是「下一个 agent 阶段」在模板里
    	          // 的 agent/model，用户没动过就不写覆盖，让后端照模板走。
    	          const taskInlineTemplate = taskTemplates.find((tpl) => tpl.id === taskInlineEdit.templateId) || null;
    	          const taskInlineTemplateAgent = firstAgentStage(taskInlineTemplate);
    	          const taskInlineHasAgentStage = !!taskInlineTemplateAgent;
    	          // 喂给 StageEditor 的那一段：user 段（角色不可切、没有段名），
    	          // 但带上「下一个 agent 阶段」的 agent/model/effort，选择器才有默认值。
    	          // auto_advance 照模板首段的来，面板上可以临时改。
    	          const createTaskInputStage: StageTemplate = {
    	            name: "",
    	            role: "user",
    	            prompt_template: taskInlineEdit.text,
    	            // 模板里第一个 agent 段没写 agent/model 时退回 claude + sonnet。
    	            agent: taskInlineEdit.agentOverride || taskInlineTemplateAgent?.agent || DEFAULT_TASK_AGENT,
    	            model: taskInlineEdit.modelOverride || taskInlineTemplateAgent?.model || DEFAULT_TASK_MODEL,
    	            effort: taskInlineEdit.effortOverride || taskInlineTemplateAgent?.effort || "",
    	            start_immediately: taskInlineEdit.startImmediately === true,
    	          };
    	          return (
            <PanelShell
              width={640}
              onClose={() => { if (!taskInlineSaving) closeTaskEditDialog(); }}
              hasUnsavedChanges={() => String(taskInlineEdit?.text || "").trim() !== ""}
              title={(
                <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "8px", minWidth: 0 }}>
                  <div style={{ fontSize: "13px", fontWeight: 800, color: "var(--text-color)", whiteSpace: "nowrap" }}>
                    {t("task.createDialogTitle", {
                      name: taskInlineEdit.templateName || t("task.defaultTitle"),
                    })}
                  </div>
                  {taskInlineEdit.allowProjectSwitch && taskCreateProjectOptions.length > 1 ? (
                    <div style={{ minWidth: "120px", maxWidth: "160px" }}>
                      <Select
                        value={taskInlineTargetRootId}
                        ariaLabel={t("task.workspaceSelectProject")}
                        onChange={(value) => switchTaskInlineEditProject(value)}
                        options={taskCreateProjectOptions}
                        size="panel"
                      />
                    </div>
                  ) : null}
                  {taskTemplates.length > 0 ? (
                    <div style={{ minWidth: "120px", maxWidth: "160px" }}>
                      <Select
                        value={taskInlineEdit.templateId}
                        ariaLabel={t("task.selectTemplate")}
                        onChange={(value) => {
                          const picked = taskTemplates.find((tpl) => tpl.id === value);
                          if (!picked) return;
                          setTaskInlineEdit((prev) => prev ? { ...prev, templateId: picked.id || "", templateName: picked.name, text: firstUserInputTemplate(picked), startImmediately: picked.stages?.[0]?.snapshot?.start_immediately === true } : prev);
                          setTaskInlineActiveToken(null);
                          setTaskInlineCandidates([]);
                        }}
                        options={taskTemplates.map((tpl) => ({ value: tpl.id || "", label: tpl.name }))}
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
                    </div>
                  ) : null}
                </div>
              )}
            >
                {taskInlineEdit.createWorktree && taskInlineEdit.worktreeBranchMode === "new" && taskInlineEdit.canToggleWorktree && <input aria-label={t("task.newBranchName")} placeholder={t("task.newBranchName")} value={taskInlineEdit.worktreeBranch} onChange={e=>setTaskInlineEdit(prev=>prev?{...prev,worktreeBranch:e.target.value}:prev)} style={{margin:"8px 12px",padding:8}}/>}
                <div style={{ padding: "12px", overflow: "visible", position: "relative", minHeight: 0, display: "flex", flexDirection: "column" }}>
                  {taskInlineActiveToken && taskInlineCandidates.length > 0 ? (
                    <div
                      style={{
                        position: "absolute",
                        left: "12px",
                        right: "12px",
                        bottom: "calc(100% + 6px)",
                        maxHeight: isMobile ? "min(42vh, 260px)" : "260px",
                        overflowY: "auto",
                        border: "1px solid var(--menu-border)",
                        borderRadius: "8px",
                        background: "var(--menu-bg)",
                        boxShadow: "0 12px 28px rgba(15, 23, 42, 0.14)",
                        zIndex: 2,
                      }}
                    >
                      {taskInlineCandidates.map((candidate, index) => (
                        <button
                          key={`${candidate.type}:${candidate.name}`}
                          ref={(node) => {
                            taskInlineCandidateItemRefs.current[index] = node;
                          }}
                          type="button"
                          onMouseDown={(event) => {
                            event.preventDefault();
                            applyTaskInlineCandidate(candidate);
                          }}
                          style={{
                            width: "100%",
                            border: "none",
                            borderTop: index === 0 ? "none" : "1px solid var(--menu-divider)",
                            background: index === taskInlineCandidateIndex ? "var(--menu-active-bg)" : "transparent",
                            color: "var(--text-primary)",
                            display: "flex",
                            flexDirection: "column",
                            alignItems: "flex-start",
                            gap: "2px",
                            padding: "9px 10px",
                            textAlign: "left",
                            cursor: "pointer",
                          }}
                          onMouseEnter={() => setTaskInlineCandidateIndex(index)}
                        >
                          <span style={{ fontSize: "13px", fontWeight: 700 }}>
                            {candidate.type === "file" ? `@${candidate.name}` : candidate.type === "prompt" ? `#${candidate.name}` : candidate.type === "slash_command" ? `/${candidate.name}` : candidate.name}
                          </span>
                          {candidate.description ? (
                            <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{candidate.description}</span>
                          ) : null}
                        </button>
                      ))}
                    </div>
                  ) : null}
                  {taskInlineEdit.previousInputs.length > 0 ? (
                    <div
                      style={{
                        marginBottom: "10px",
                        display: "flex",
                        flexDirection: "column",
                        gap: "8px",
                        maxHeight: isMobile ? "18dvh" : "180px",
                        overflowY: "auto",
                      }}
                    >
                      {taskInlineEdit.previousInputs.map((item) => (
                        <div
                          key={item.id}
                          style={{
                            border: "1px solid var(--border-color)",
                            borderRadius: "8px",
                            background: "rgba(100, 116, 139, 0.08)",
                            padding: "8px 9px",
                          }}
                        >
                          <div
                            style={{
                              marginBottom: "5px",
                              fontSize: "11px",
                              fontWeight: 800,
                              color: "var(--text-secondary)",
                            }}
                          >
                            {item.label}
                          </div>
                          <div
                            style={{
                              whiteSpace: "pre-wrap",
                              wordBreak: "break-word",
                              fontSize: "12px",
                              lineHeight: 1.45,
                              color: "var(--text-color)",
                            }}
                          >
                            <InlineTokenText content={item.input} />
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : null}
                  <StageEditor
                    editorRef={taskInlineEditorRef}
                    stage={createTaskInputStage}
                    agents={availableAgents}
                    isFirstStage
                    /* 任务名和 user 开关同排：以前这个输入框在 StageEditor 外面
                       自己占一行，user 芯片被挤到下一排去了。样式照抄模板编辑
                       面板的段名输入框。 */
                    leading={(
                      <input
                        value={taskInlineEdit.name}
                        onChange={(event) => setTaskInlineEdit((prev) => prev ? { ...prev, name: event.target.value } : prev)}
                        placeholder={t("task.namePlaceholder")}
                        style={{ ...composerInputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontWeight: 800 }}
                      />
                    )}
                    onChange={(patch) => setTaskInlineEdit((prev) => prev ? {
                      ...prev,
                      text: patch.prompt_template ?? prev.text,
                      ...(patch.agent ? { agentOverride: patch.agent } : {}),
                      ...(patch.model !== undefined ? { modelOverride: patch.model } : {}),
                      ...(patch.effort !== undefined ? { effortOverride: patch.effort } : {}),
                      // 首段的「立即执行」：面板上可临时改，勾上创建后直接开跑。
                      ...(patch.start_immediately !== undefined ? { startImmediately: patch.start_immediately } : {}),
                    } : prev)}
                    /* 字段说明跟模板编辑面板共用一套：新建任务这里填的就是模板里
                       那个「用户输入模板」的预填内容。 */
                    label={(
                      <FieldLabelWithInfo
                        label={t("taskTemplate.userInputTemplate")}
                        info={t("taskTemplate.userInputTemplateInfo")}
                        helpKey="task-input"
                        openHelpKey={taskInlineHelpKey}
                        setOpenHelpKey={setTaskInlineHelpKey}
                      />
                    )}
                    /* 这个编辑区本身是 user 段，但新建时要在这里挑「下一个 agent 阶段」
                       用哪个 agent/模型，所以把 PromptEditor 自带的 AgentSelector 打开。
                       默认值取模板里第一个 agent 段的 agent/model（createTaskInputStage）。 */
                    showAgentSelector={taskInlineHasAgentStage}
                    /* 附件「+」：面板底部的隐藏 file input 一直在（App.tsx:9782），
                       chips 也在渲染，只是没告诉编辑器有这个按钮可点。
                       任务详情面板（TaskDetailPanel.tsx:445）早就这么传了。 */
                    onAttach={() => taskInlineAttachmentInputRef.current?.click()}
                    onSend={() => void saveTaskInlineEdit()}
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
