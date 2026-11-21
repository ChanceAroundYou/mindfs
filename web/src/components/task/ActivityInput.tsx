import { useRef, useState } from "react";
import type React from "react";
import { useI18n } from "../../i18n/index";
import type { CandidateItem } from "../../shared/candidateTypes";
import type { WorkflowChoice } from "../../shared/taskComments";
import type { ComposerAttachment, UploadProgress } from "../../shared/uploadTypes";
import { canSendActivity } from "../../shared/activityModel";
import { DEFAULT_TASK_AGENT } from "../../app/appTask";
import { useComposerCandidates } from "../../hooks/useComposerCandidates";
import { TaskInlineCandidateMenu } from "../app/TaskInlineCandidateMenu";
import type { TokenEditorHandle } from "../editor/TokenEditor";
import type { ActiveToken } from "../editor/tokenEditorUtils";
import { getClipboardImageFiles } from "../editor/pasteSupport";
import { useCandidateKeyboard } from "../editor/useCandidateKeyboard";
import { PromptEditor } from "../editor/PromptEditor";
import { ComposerAttachmentStrip } from "../editor/ComposerAttachmentStrip";
import { WorkflowSelector } from "./WorkflowSelector";

export type ActivityInputProps = {
  body: string;
  onBodyChange: (body: string) => void;
  /** 候选（@ 文件 / # 提示词 / / 命令）按这个项目与节点拉取。 */
  rootId: string;
  nodeId?: string;
  choice: WorkflowChoice;
  versionId: string;
  onWorkflowChange: (choice: WorkflowChoice, versionId: string) => void;
  /** 待发送附件（与新建任务面板同一份模型）。 */
  attachments?: ComposerAttachment[];
  onAttachFiles?: (files: File[]) => void;
  onRemoveAttachment?: (id: string) => void;
  uploadProgress?: UploadProgress | null;
  onCancelUpload?: () => void;
  sending?: boolean;
  error?: string | null;
  placeholder?: string;
  /** 每次发送成功后由父层递增，把编辑器清空。 */
  resetKey?: number;
  onSend: (text: string) => void;
};

/**
 * 对话输入框：受控组件，状态由 ConversationView 持有。
 *
 * 与新建任务面板共用 PromptEditor（Lexical token 编辑器）与附件条：圆角、字号、
 * 回车行为、添加文件按钮、发送键位置都一致；@ 文件 / # 提示词 / / 命令候选与
 * 会话输入框同一套（↑↓ 高亮、Tab/Enter 选中、Esc 关闭），粘贴截图直接进附件条。
 * 工作流选择器作为右下角控件行的第三个按钮（图标）挂在编辑器里，不再在下面另起
 * 一行三态面板。
 *
 * 视图层不碰 services（仓库硬规则），上传在 useTaskActivities 里做、候选拉取在
 * useComposerCandidates 里做；发送动作通过 onSend 回调交给 hook；禁用条件统一走
 * canSendActivity，避免「按钮能点但请求必然 400」的假可用。
 */
export function ActivityInput({
  body,
  onBodyChange,
  rootId,
  nodeId,
  choice,
  versionId,
  onWorkflowChange,
  attachments = [],
  onAttachFiles,
  onRemoveAttachment,
  uploadProgress,
  onCancelUpload,
  sending = false,
  error,
  placeholder,
  resetKey,
  onSend,
}: ActivityInputProps) {
  const { t } = useI18n();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const editorRef = useRef<TokenEditorHandle | null>(null);
  // 候选菜单 portal 到 body，按这层容器的矩形定位（见 TaskInlineCandidateMenu）。
  const composerAnchorRef = useRef<HTMLDivElement | null>(null);
  const [activeToken, setActiveToken] = useState<ActiveToken | null>(null);
  const {
    candidates,
    setCandidates,
    activeCandidateIndex,
    setActiveCandidateIndex,
    candidateItemRefs,
  } = useComposerCandidates({
    rootId,
    nodeId,
    agent: DEFAULT_TASK_AGENT,
    activeToken,
  });

  const canSend = canSendActivity({
    body,
    workflowChoice: choice,
    workflowVersionId: versionId,
    sending,
    hasAttachments: attachments.length > 0,
  });

  const applyCandidate = (candidate: CandidateItem) => {
    setCandidates([]);
    setActiveCandidateIndex(0);
    setActiveToken(null);
    editorRef.current?.insertCandidate(candidate.type, candidate.name);
    editorRef.current?.focus();
  };

  const { handleCandidateKeyDown, handleCandidateEnter } = useCandidateKeyboard({
    candidates,
    activeCandidateIndex,
    setActiveCandidateIndex,
    setCandidates,
    applyCandidate,
  });

  const handlePaste = (event: React.ClipboardEvent<HTMLDivElement>) => {
    if (sending || !onAttachFiles) return;
    const imageFiles = getClipboardImageFiles(event);
    if (imageFiles.length === 0) return;
    event.preventDefault();
    onAttachFiles(imageFiles);
  };

  // 发送入口从编辑器读最新文本：输入后立刻回车时，父层 body 可能还没回流。
  const handleSend = (text: string) => {
    const latest = text || body;
    if (!canSendActivity({
      body: latest,
      workflowChoice: choice,
      workflowVersionId: versionId,
      sending,
      hasAttachments: attachments.length > 0,
    })) return;
    onBodyChange(latest);
    onSend(latest);
  };

  return (
    <div
      data-testid="activity-input"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div ref={composerAnchorRef} style={{ position: "relative" }}>
        <TaskInlineCandidateMenu
          visible={!!activeToken}
          anchorRef={composerAnchorRef}
          candidates={candidates}
          selectedIndex={activeCandidateIndex}
          itemRefs={candidateItemRefs}
          onSelect={applyCandidate}
          onSelectedIndexChange={setActiveCandidateIndex}
        />
        <PromptEditor
          ref={editorRef}
          value={body}
          onChange={onBodyChange}
          onActiveTokenChange={setActiveToken}
          onKeyDown={handleCandidateKeyDown}
          onEnter={handleCandidateEnter}
          onPaste={handlePaste}
          resetKey={resetKey}
          placeholder={placeholder ?? t("task.activity.inputPlaceholder")}
          onAttach={onAttachFiles ? () => fileInputRef.current?.click() : undefined}
          onSend={handleSend}
          sending={sending}
          sendDisabled={!canSend}
          sendTestId="activity-send"
          sendLabel={sending ? t("task.activity.sending") : t("task.activity.send")}
          extraControls={(
            <WorkflowSelector
              choice={choice}
              versionId={versionId}
              onChange={onWorkflowChange}
            />
          )}
          extraControlsExtraInset={0}
        />
      </div>

      {onAttachFiles ? (
        <input
          ref={fileInputRef}
          data-testid="activity-attachment-input"
          type="file"
          multiple
          style={{ display: "none" }}
          onChange={(event) => {
            const files = Array.from(event.target.files || []);
            if (files.length > 0) onAttachFiles(files);
            event.currentTarget.value = "";
          }}
        />
      ) : null}

      <ComposerAttachmentStrip
        attachments={attachments}
        uploadProgress={uploadProgress}
        disabled={sending}
        onRemove={(id) => onRemoveAttachment?.(id)}
        onCancelUpload={onCancelUpload}
      />

      {error && (
        <div data-testid="input-error" style={{ fontSize: 12, color: "#dc2626", padding: "4px 0" }}>
          {error}
        </div>
      )}
    </div>
  );
}
