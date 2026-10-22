import React from "react";
import { ModeSelector, type SessionMode } from "../agent/ModeSelector";
import { AgentSelector } from "../agent/AgentSelector";
import type { AgentStatus } from "../../shared/agentTypes";
import type { CandidateItem } from "../../shared/candidateTypes";
import TokenEditor, { type TokenEditorHandle } from "../editor/TokenEditor";
import { useI18n } from "../../i18n/index";
import {
  CandidateDropdown,
} from "./CandidateDropdown";
import {
  AttachmentsArea,
  type PendingAttachment,
} from "./AttachmentsArea";
import { QueuedMessagesList } from "./QueuedMessagesList";
import { ShellSelector } from "./ShellSelector";
import { ActionBarControls } from "./ActionBarControls";
import { ComposerTopChips } from "./ComposerTopChips";
import { SessionRing } from "./SessionRing";
import {
  appAccentHexToRgba,
  chatBlurPlaceholderKeys,
  IME_ENTER_GUARD_MS,
} from "../../shared/actionBarUtils";
import { DEFAULT_NODE_COLOR } from "../../shared/nodePalette";
import { type ActionBarProps, type AttachedFileContext, type QueuedMessageInfo, type SessionInfo, type WSStatus } from "./actionTypes";
import { useActionBar } from "./useActionBar";
import { useResponsive } from "../../hooks/useResponsive";

export type {
  ActionBarProps,
  AttachedFileContext,
  QueuedMessageInfo,
  SessionInfo,
  WSStatus,
} from "./actionTypes";

export function ActionBar(props: ActionBarProps) {
  const { t } = useI18n();
  const {
    status = "disconnected",
    agentsVersion = 0,
    codexRateLimitsRefreshToken = 0,
    currentRootId,
    currentRootIsGitRepo = false,
    currentSession,
    pendingPlanMode = false,
    rootColor = null,
    attachedFileContext,
    canOpenSessionDrawer = false,
    hideComposer = false,
    sessionDrawerOpen = false,
    detachedBoundSession = false,
    editDraftRequest = null,
    queuedMessages = [],
    inputHistory = [],
    onSendMessage,
    onSetPlanMode,
    onCancelCurrentTurn,
    onRemoveQueuedMessage,
    onUpdateQueuedMessage,
    onSendQueuedMessageNow,
    onNewSession,
    onSelectProject,
    onSelectSession,
    onRequestFileContext,
    onClearFileContext,
    onSessionClick,
    mobileEnterKeySends = false,
    sendShortcut = null,
  } = props;

  const {
    mode, setMode,
    agent, setAgent,
    model, setModel,
    agentMode, setAgentMode,
    effort, setEffort,
    fastService, setFastService,
    longContext, setLongContext,
    agents, setAgents, handleAgentRestart,
    shells, setShells,
    shell, setShell,
    serializedInput, setSerializedInput,
    activeToken, setActiveToken,
    sending, setSending,
    uploadProgress, setUploadProgress,
    cancelling,
    isFocused, setIsFocused,
    blurPlaceholderKey, setBlurPlaceholderKey,
    agentSelectorOpen, setAgentSelectorOpen,
    modeSelectorOpen, setModeSelectorOpen,
    ringDragging, setRingDragging,
    candidates, setCandidates,
    activeCandidateIndex, setActiveCandidateIndex,
    candidateItemRefs,
    bumpPromptCandidateRefresh,
    pendingAttachments,
    appendPendingAttachments,
    removePendingAttachment,
    clearPendingAttachments,
    createWorktree, setCreateWorktree,
    worktreeBranchMode, setWorktreeBranchMode,
    worktreeBranch, setWorktreeBranch,
    worktreeBranches, worktreeBranchesLoading, worktreeBranchError,
    isMobile,
    isDark,
    editorRef,
    attachmentInputRef,
    uploadAbortRef,
    isComposingRef,
    compositionGuardUntilRef,
    selectedAgent,
    selectedModelInfo,
    availableEfforts,
    supportsEffort,
    supportsServiceTier,
    supportsLongContext,
    effectiveModelForSend,
    planModeActive,
    planSessionKey,
    planRootId,
    isMultiLine,
    syncEditorHeight,
    resetEditorHeight,
    handleEditorChange,
    applyCandidate,
    removePromptLocally,
    handleSend,
    handleCancel,
    handleKeyDown,
    handleEditorEnter,
    handleEditorPaste,
    resetForNewSession,
    isSelectedAgentUnavailable,
    canSend,
    hasBoundSession,
    hasDraft,
    showCancel,
    isModeLocked,
    inputPlaceholder,
    editorInsets,
    editorMinHeight,
    connectionMeta,
    accentColorRaw,
    accentHex,
  } = useActionBar(props);

  // 看板/工作台：不渲染输入区。移动端呼出左右侧栏的按钮已搬进 AppShell 的顶栏。
  if (hideComposer) {
    return null;
  }

  return (
    <div data-onboarding="action-bar" style={{ width: "100%", minWidth: 0, padding: isMobile ? "0 8px var(--mindfs-actionbar-bottom-padding, calc(env(safe-area-inset-bottom, 0px) + 2px))" : "0 16px 12px", display: "flex", justifyContent: "center", boxSizing: "border-box", background: "var(--content-bg)" }}>
      <div style={{ position: "relative", width: "100%", minWidth: 0, display: "flex", flexDirection: "column", gap: 0 }}>
        <ComposerTopChips
          planModeActive={planModeActive}
          planSessionKey={planSessionKey}
          planRootId={planRootId}
          agentSelectorOpen={agentSelectorOpen}
          modeSelectorOpen={modeSelectorOpen}
          isMobile={isMobile}
          currentSession={currentSession}
          currentRootIsGitRepo={currentRootIsGitRepo}
          mode={mode}
          agent={agent}
          accentHex={accentHex}
          sending={sending}
          createWorktree={createWorktree}
          worktreeBranchMode={worktreeBranchMode}
          worktreeBranch={worktreeBranch}
          worktreeBranches={worktreeBranches}
          worktreeBranchesLoading={worktreeBranchesLoading}
          worktreeBranchError={worktreeBranchError}
          agentsVersion={agentsVersion}
          codexRateLimitsRefreshToken={codexRateLimitsRefreshToken}
          currentRootId={currentRootId}
          onTogglePlanMode={(enabled) => {
            void onSetPlanMode?.(enabled, planSessionKey, planRootId);
          }}
          onToggleWorktree={() => setCreateWorktree((value) => !value)}
          onWorktreeBranchChange={(branchMode, branch) => {
            setWorktreeBranchMode(branchMode);
            setWorktreeBranch(branch);
          }}
        />
        {queuedMessages.length > 0 ? (
          <QueuedMessagesList
            queuedMessages={queuedMessages}
            accentColorRaw={accentColorRaw}
            isMobile={isMobile}
            onRemove={(id) => onRemoveQueuedMessage?.(id)}
            onUpdate={(id, content) => onUpdateQueuedMessage?.(id, content)}
            onSendNow={(id) => onSendQueuedMessageNow?.(id)}
          />
        ) : null}
        <div style={{ display: "grid", gridTemplateColumns: "1fr", alignItems: "center", gap: 0, padding: 0, minWidth: 0, maxWidth: "100%" }}>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-start",
              gap: 0,
              minWidth: 0,
            }}
          >
            <div
              data-mindfs-command-input-width="1"
              data-onboarding="message-input"
              style={{
                background: "var(--panel-bg)",
                border: isFocused
                  ? `1px solid ${accentColorRaw}`
                  : "1px solid var(--panel-border)",
                borderRadius: isMobile ? "10px" : "12px",
                boxShadow: isMobile
                  ? "none"
                  : (isFocused ? `0 0 0 3px ${appAccentHexToRgba(accentHex, isDark ? 0.2 : 0.1)}` : "var(--panel-shadow)"),
                display: "flex",
                alignItems: "center",
                position: "relative",
                minHeight: `${editorMinHeight}px`,
                transition: ringDragging ? "none" : "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)",
                minWidth: 0,
                width: "100%",
                overflow: "visible",
              }}
            >
              <TokenEditor
                ref={editorRef}
                placeholder={inputPlaceholder}
                disabled={sending}
                isDark={isDark}
                rightInset={editorInsets.rightInset}
                topInset={0}
                bottomInset={editorInsets.bottomInset}
                onChange={handleEditorChange}
                onFocusChange={(focused) => {
                  setIsFocused(focused);
                  if (!focused && mode === "chat") {
                    setBlurPlaceholderKey(
                      chatBlurPlaceholderKeys[Math.floor(Math.random() * chatBlurPlaceholderKeys.length)] || "action.placeholder.chat",
                    );
                  }
                  if (focused) {
                    if (mode === "command" && serializedInput.trim()) {
                      setActiveToken({ type: "command", query: serializedInput.trim() });
                    }
                    onRequestFileContext?.();
                  }
                }}
                onPointerDown={onRequestFileContext}
                onKeyDown={handleKeyDown}
                onPaste={handleEditorPaste}
                onEnter={handleEditorEnter}
                enterKeyHint={isMobile && mobileEnterKeySends && mode === "chat" ? "send" : undefined}
                onCompositionStart={() => {
                  isComposingRef.current = true;
                  compositionGuardUntilRef.current = 0;
                }}
                onCompositionEnd={() => {
                  isComposingRef.current = false;
                  compositionGuardUntilRef.current = performance.now() + IME_ENTER_GUARD_MS;
                }}
              />

              {activeToken && (candidates.length > 0 || activeToken.type === "prompt") ? (
                <CandidateDropdown
                  candidates={candidates}
                  activeCandidateIndex={activeCandidateIndex}
                  candidateItemRefs={candidateItemRefs}
                  isDark={isDark}
                  isMobile={isMobile}
                  activeTokenType={activeToken.type}
                  mode={mode}
                  accentColorRaw={accentColorRaw}
                  accentHex={accentHex}
                  applyCandidate={applyCandidate}
                  onPromptSaved={() => bumpPromptCandidateRefresh()}
                  onPromptDeleted={removePromptLocally}
                  onPromptFormClosed={() => {
                    requestAnimationFrame(() => editorRef.current?.focus());
                  }}
                />
              ) : null}

              <span
                aria-label={connectionMeta.label}
                title={connectionMeta.label}
                style={{
                  position: "absolute",
                  left: "5px",
                  bottom: "4px",
                  width: "6px",
                  height: "6px",
                  borderRadius: "50%",
                  background: connectionMeta.color,
                  boxShadow: connectionMeta.shadow,
                  pointerEvents: "auto",
                  zIndex: 6,
                }}
              />

              <ActionBarControls
                isMobile={isMobile}
                isMultiLine={isMultiLine}
                hasBoundSession={hasBoundSession}
                canOpenSessionDrawer={canOpenSessionDrawer}
                sessionDrawerOpen={sessionDrawerOpen}
                detachedBoundSession={detachedBoundSession}
                accentColorRaw={accentColorRaw}
                accentHex={accentHex}
                currentSession={currentSession}
                currentRootId={currentRootId}
                onSessionClick={onSessionClick}
                onDraggingChange={setRingDragging}
                onNewSession={onNewSession}
                resetForNewSession={resetForNewSession}
                onSelectProject={onSelectProject}
                onSelectSession={onSelectSession}
                mode={mode}
                setMode={setMode}
                isModeLocked={isModeLocked}
                setModeSelectorOpen={setModeSelectorOpen}
                agent={agent}
                model={model}
                agentMode={agentMode}
                effort={effort}
                longContext={longContext}
                fastService={fastService}
                agents={agents}
                setAgent={setAgent}
                setModel={setModel}
                setAgentMode={setAgentMode}
                setEffort={setEffort}
                setFastService={setFastService}
                setLongContext={setLongContext}
                setAgents={setAgents}
                onAgentRestart={handleAgentRestart}
                isSelectedAgentUnavailable={isSelectedAgentUnavailable}
                setAgentSelectorOpen={setAgentSelectorOpen}
                shell={shell}
                shells={shells}
                setShell={setShell}
                sending={sending}
                pendingAttachments={pendingAttachments}
                attachmentInputRef={attachmentInputRef}
                appendPendingAttachments={appendPendingAttachments}
                showCancel={showCancel}
                handleCancel={handleCancel}
                handleSend={handleSend}
                cancelling={cancelling}
                canSend={canSend}
                t={t}
              />
            </div>
          </div>
        </div>
        <AttachmentsArea
          attachedFileContext={attachedFileContext}
          pendingAttachments={pendingAttachments}
          uploadProgress={uploadProgress}
          isDark={isDark}
          isMobile={isMobile}
          accentHex={accentHex}
          onCancelUpload={() => uploadAbortRef.current?.abort()}
          onClearFileContext={onClearFileContext}
          onRemoveAttachment={removePendingAttachment}
        />
      </div>
      <style>{`
        @keyframes spin {
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}
