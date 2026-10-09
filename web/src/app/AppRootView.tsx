/**
 * App 的根视图组合：外壳 + 全局浮层（Onboarding / E2EE / 任务内联编辑 / 任务会话错误 /
 * 定时任务 / 任务模板 / Toast / DialogHost）。
 *
 * 见 docs/plans/app-tsx-under-1000.md。原先这整段 JSX 直接写在 `App()` 的 return 里，
 * 是 App.tsx 冲到 4182 行的主因；现在整段搬到这里，`App()` 只负责把四层聚合 hook
 * 的返回值展开进来。
 *
 * props 类型由四层 hook 的返回值交集推导，不手写 200 多个字段 ——
 * 新增/删除返回值时这里不会漏。四层返回值之间没有同名属性（已用 tsc 实测），
 * 所以交集不会退化成 never。
 */
import { reportError } from "../services/net/error";
import {
  fetchFile,
  fetchEditableFile,
  saveTextFile,
  clearFileCacheForRoot,
  clearFileMemoryCacheForView,
  getCachedFile,
  type FilePayload,
} from "../services/file";
import { copyText } from "../services/platform/clipboard";
import { AppShell } from "../layout/AppShell";
import { ToastContainer } from "../components/shell/Toast";
import { DialogHost } from "../components/shell/DialogHost";
import { BottomSheet } from "../components/shell/BottomSheet";
import { ScheduledAgentTaskDialog } from "../components/task/ScheduledAgentTaskDialog";
import { TaskTemplateDialog, FieldLabelWithInfo } from "../components/task/TaskTemplateDialog";
import { PanelShell } from "../components/shell/PanelShell";
import { composerInputStyle } from "../components/action/composerStyles";
import {
  completeOnboarding,
  dismissOnboarding,
  shouldAutoStartOnboarding,
} from "../services/prefs/onboarding";
import { getDirectorySortKey, normalizeComparableRootPath, normalizeTreeResponse, unscopedRootId } from "../app/appUtils";
import { hasSessionExchanges, isSessionShownInMain, isTopLevelSessionItem, normalizeMode, relatedFileSelectionKey, sessionInputHistory, shouldAutoSelectNewSession, toSessionItem } from "../app/appSession";
import { accountScopedKey, loadGitDiffSideBySide, loadLastRootId, loadLastRootNodeId, loadMobileEnterKeySends, loadPersistedFileScrollPositions, loadPersistedPluginQuery, loadSidebarsSwapped, loadTaskCreateWorktreePreference, persistFileScrollPositions, persistPluginQuery, removeLocalStorageByPrefix, saveTaskCreateWorktreePreference } from "../app/appStorage";
import { applyStageOverride, currentTaskInputFromDetail, DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, firstAgentStage, firstTaskInputFromDetail, firstUserInputTemplate, isTerminalKanbanTask, latestTaskStageRun, normalizeFastService, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, previousTaskInputsFromDetail, taskSessionKeysFromDetail, taskStagesForCreate, taskStatusLabel } from "../app/appTask";
import { CheckIconSmall, PlusSmallIcon, taskCardIconButtonStyle } from "../app/taskIcons";
import { TaskSessionErrorDialog } from "../components/app/TaskSessionErrorDialog";
import { TaskDetailPanelGate } from "../components/app/TaskDetailPanelGate";
import { E2eePrompt } from "../components/app/E2eePrompt";
import { OnboardingGate } from "../components/app/OnboardingGate";
import { TaskInlineEditPanel } from "../components/app/TaskInlineEditPanel";
import { SessionSheet } from "../components/app/SessionSheet";
import { AppActionBar } from "../components/app/AppActionBar";
import { SessionPane } from "../components/app/SessionPane";
import { FileTreePanel } from "../components/app/FileTreePanel";
import { renderRootSlashCommandResult } from "../app/renderRootSlashCommandResult";
import type { useAppStateLayer } from "./useAppStateLayer";
import type { useAppSessionLayer } from "./useAppSessionLayer";
import type { useAppDomainLayer } from "./useAppDomainLayer";
import type { useAppViewComposition } from "./useAppViewComposition";

export type AppRootViewProps =
  ReturnType<typeof useAppStateLayer> &
  ReturnType<typeof useAppSessionLayer> &
  ReturnType<typeof useAppDomainLayer> &
  ReturnType<typeof useAppViewComposition> & {
    onGoHome?: () => void;
  };

export function AppRootView(props: AppRootViewProps) {
  // App 自己的 prop，不来自任何一层
  const { onGoHome } = props;

  // useAppStateLayer 返回值中本组件用到的部分
  const {
    agentConfigSwitchRequest, agentsVersion, applyTaskDetails, applyTaskInlineCandidate,
    attachedFileContext, availableAgents, bootstrapState, closeTaskEditDialog,
    codexRateLimitsRefreshToken, copiedSlashCommandKeys, currentDirectorySortMode, currentDirectorySortOverride,
    currentFileEditing, currentMainContentView, currentRootDisplayName, currentRootId,
    currentRootIdRef, currentRootNodeId, currentSession, directoryUploadAbortRef,
    directoryUploadProgress, drawerLoadingSessionByRoot, drawerOpenByRootRef, drawerScrollRef,
    e2eePromptBusy, e2eePromptError, e2eeSecretInput, e2eeState,
    editDraftRequest, entriesByPath, expanded, file,
    fileEditStore, fileRef, fileScrollPositionsRef, fontSizePreferences,
    getDisplayNodeColor, getNodeIdForRoot, getRootDisplayName, gitDiff,
    gitDiffSideBySide, handleMoveKanbanTask, handleTaskInlineAttachmentChange, handleTaskTemplateSaved,
    interactionModeRef, isDrawerOpen, isLeftOpen, isMobile,
    isRightOpen, mainDirectoryError, mainView, mainViewRef,
    managedRootByIdRef, mobileEnterKeySends, onboardingOpen, pendingPlanMode,
    pluginBypass, pluginContentRef, pluginLoading, projectTreeTabRequest,
    refreshGitStatus, removeTaskInlineAttachment, rootEntries, saveTaskInlineEdit,
    scheduledAgentDialogOpen, scopedRootKey, selectedDir, selectedDirKey,
    selectedDirRef, selectedKanbanTaskId, selectedSession, sendShortcut,
    setCodexRateLimitsRefreshToken, setCopiedSlashCommandKeys, setDirectorySortOverrides, setDrawerOpenForRoot,
    setE2eePromptError, setE2eeSecretInput, setEntriesByPath, setFile,
    setFontSizePreferences, setGitDiffSideBySide, setInteractionMode, setIsLeftOpen,
    setIsRightOpen, setMobileEnterKeySends, setOnboardingMainContentViewRoot, setOnboardingOpen,
    setPluginBypass, setProjectTreeTab, setScheduledAgentDialogOpen, setSelectedKanbanTaskId,
    setSendShortcut, setSidebarsSwapped, setTaskInlineActiveToken, setTaskInlineCandidateIndex,
    setTaskInlineCandidates, setTaskInlineEdit, setTaskInlineHelpKey, setTaskSessionErrorDialog,
    setTaskTemplateDialogOpen, setTreeSortMode, showHiddenFiles, showSessionPane,
    sidebarsSwapped, slashCopyResetTimersRef, status, switchMainView,
    switchTaskInlineEditProject, t, taskCreateProjectOptions, taskDetailsById,
    taskInlineActiveToken, taskInlineAttachmentInputRef, taskInlineCandidateIndex, taskInlineCandidateItemRefs,
    taskInlineCandidates, taskInlineEdit, taskInlineEditorRef, taskInlineHelpKey,
    taskInlineSaving, taskInlineUploadAbortRef, taskInlineUploadProgress, taskSessionErrorDialog,
    taskTemplateDialogOpen, taskTemplateDialogTemplate, taskTemplates, taskWorktreeBranches,
    treeSortMode, workspaceOpen,
  } = props;
  // useAppSessionLayer 返回值中本组件用到的部分
  const {
    handleCancelCurrentTurn, handleCreateBlankFile, handleForkAgentMessage, handleNewSession,
    handleOnboardingStepChange, handleRestartAgent, handleRunAgentLifecycleCommand, handleSelectSession,
    handleSendMessage, handleSetPlanMode, handleStartUpdate, handleTreeUpload,
    refreshTreeDir, submitE2EESecret,
  } = props;
  // useAppDomainLayer 返回值中本组件用到的部分
  const {
    actionBarInputHistory, actionBarQueuedMessages, actionBarSession, actionHandlers,
    canOpenSessionDrawer, creatingRootBusy, creatingRootKind, creatingRootName,
    detachedBoundSession, gitFileStatsByPath, handleClearFileContext, handleCreateRootCancel,
    handleCreateRootStart, handleCreateRootSubmit, handleDirectoryPathClick, handleDisablePendingPlugins,
    handleFileViewerFileClick, handleFileViewerPathClick, handleGitDiffPathClick, handleOpenProjectAdd,
    handleOpenWorktreeLocation, handleRemoveCurrentRoot, handleRemoveCurrentWorktree, handleRemoveQueuedMessage,
    handleRenameCurrentRoot, handleRequestFileContext, handleSendQueuedMessageNow, handleSessionChipClick,
    handleSwitchWorktreeStart, handleTaskSessionDrawerOpen, handleTrustPendingPlugins, handleUpdateQueuedMessage,
    handleViewerSelectionChange, matchedPlugin, pluginHandlers, pluginRender,
    pluginRendererKey, pluginThemeVars, projectAddMode, projectAddOverlay,
    selectedKanbanTask, setCreatingRootName, visibleMainEntries, worktreeCreateOverlay,
    worktreeSwitchOpen, worktreeSwitchOverlay,
  } = props;
  // useAppViewComposition 返回值中本组件用到的部分
  const {
    activePendingPluginTrust, currentFileScrollKey, currentRootSlashCommandResult, drawerSessionSnapshot,
    handleAskUserAnswer, handleDrawerSessionFileClick, handleEditUserMessage, handleMainViewSwitcherChange,
    handleNodeManagerRefresh, handleProjectTreeRefresh, handleRemoveSessionRelatedFile, kanbanTaskPanel,
    renderRootGitContent, renderRootRelatedContent, renderRootWorktreeContent, rootSessionIndicators,
    sessionSidebar, sessionView, sessionViewerComposerOverlayInset, showUpdateButton,
    slashCommandResultForSession, switchToPluginView, switchToRawFileView, updateBusy,
    updateHelp, updateLabel, updateSummary, workspaceBoard,
  } = props;

  return (
    <>
      <AppShell
        leftOpen={isLeftOpen}
        rightOpen={isRightOpen}
        sidebarsSwapped={sidebarsSwapped}
        fileSidebarFontScale={fontSizePreferences.fileSidebar}
        mainFontScale={fontSizePreferences.main}
        sessionSidebarFontScale={fontSizePreferences.sessionSidebar}
        onCloseLeft={() => setIsLeftOpen(false)}
        onCloseRight={() => setIsRightOpen(false)}
        onOpenLeft={() => setIsLeftOpen(true)}
        onOpenRight={() => setIsRightOpen(true)}
sidebar={<
        FileTreePanel
        actionHandlers={actionHandlers}
        agentConfigSwitchRequest={agentConfigSwitchRequest}
        creatingRootBusy={creatingRootBusy}
        creatingRootKind={creatingRootKind}
        creatingRootName={creatingRootName}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentRootNodeId={currentRootNodeId}
        entriesByPath={entriesByPath}
        expanded={expanded}
        file={file}
        fontSizePreferences={fontSizePreferences}
        getDisplayNodeColor={getDisplayNodeColor}
        gitDiffSideBySide={gitDiffSideBySide}
        handleCreateRootCancel={handleCreateRootCancel}
        handleCreateRootStart={handleCreateRootStart}
        handleCreateRootSubmit={handleCreateRootSubmit}
        handleMainViewSwitcherChange={handleMainViewSwitcherChange}
        handleNodeManagerRefresh={handleNodeManagerRefresh}
        handleOpenProjectAdd={handleOpenProjectAdd}
        handleProjectTreeRefresh={handleProjectTreeRefresh}
        handleRestartAgent={handleRestartAgent}
        handleRunAgentLifecycleCommand={handleRunAgentLifecycleCommand}
        handleStartUpdate={handleStartUpdate}
        isMobile={isMobile}
        mainView={mainView}
        mainViewRef={mainViewRef}
        mobileEnterKeySends={mobileEnterKeySends}
        onGoHome={onGoHome}
        projectAddMode={projectAddMode}
        projectAddOverlay={projectAddOverlay}
        projectTreeTabRequest={projectTreeTabRequest}
        renderRootGitContent={renderRootGitContent}
        renderRootRelatedContent={renderRootRelatedContent}
        renderRootWorktreeContent={renderRootWorktreeContent}
        rootEntries={rootEntries}
        rootSessionIndicators={rootSessionIndicators}
        selectedDirKey={selectedDirKey}
        sendShortcut={sendShortcut}
        setCodexRateLimitsRefreshToken={setCodexRateLimitsRefreshToken}
        setCreatingRootName={setCreatingRootName}
        setFontSizePreferences={setFontSizePreferences}
        setGitDiffSideBySide={setGitDiffSideBySide}
        setIsLeftOpen={setIsLeftOpen}
        setMobileEnterKeySends={setMobileEnterKeySends}
        setProjectTreeTab={setProjectTreeTab}
        setSendShortcut={setSendShortcut}
        setSidebarsSwapped={setSidebarsSwapped}
        setTreeSortMode={setTreeSortMode}
        showHiddenFiles={showHiddenFiles}
        showUpdateButton={showUpdateButton}
        sidebarsSwapped={sidebarsSwapped}
        switchMainView={switchMainView}
        treeSortMode={treeSortMode}
        updateBusy={updateBusy}
        updateHelp={updateHelp}
        updateLabel={updateLabel}
        updateSummary={updateSummary}
        />}
        rightSidebar={sessionSidebar}
main={<
        SessionPane
        sessionView={sessionView}
        showSessionPane={showSessionPane}
        actionHandlers={actionHandlers}
                activePendingPluginTrust={activePendingPluginTrust}
                clearFileCacheForRoot={clearFileCacheForRoot}
                currentDirectorySortMode={currentDirectorySortMode}
                currentDirectorySortOverride={currentDirectorySortOverride}
                currentFileEditing={currentFileEditing}
                currentFileScrollKey={currentFileScrollKey}
                currentMainContentView={currentMainContentView}
                currentRootDisplayName={currentRootDisplayName}
                currentRootId={currentRootId}
                currentRootIdRef={currentRootIdRef}
                directoryUploadAbortRef={directoryUploadAbortRef}
                directoryUploadProgress={directoryUploadProgress}
                file={file}
                fileEditStore={fileEditStore}
                fileRef={fileRef}
                fileScrollPositionsRef={fileScrollPositionsRef}
                getDirectorySortKey={getDirectorySortKey}
                getDisplayNodeColor={getDisplayNodeColor}
                getRootDisplayName={getRootDisplayName}
                gitDiff={gitDiff}
                gitDiffSideBySide={gitDiffSideBySide}
                handleCreateBlankFile={handleCreateBlankFile}
                handleDirectoryPathClick={handleDirectoryPathClick}
                handleDisablePendingPlugins={handleDisablePendingPlugins}
                handleFileViewerFileClick={handleFileViewerFileClick}
                handleFileViewerPathClick={handleFileViewerPathClick}
                handleGitDiffPathClick={handleGitDiffPathClick}
                handleOpenWorktreeLocation={handleOpenWorktreeLocation}
                handleRemoveCurrentRoot={handleRemoveCurrentRoot}
                handleRemoveCurrentWorktree={handleRemoveCurrentWorktree}
                handleRenameCurrentRoot={handleRenameCurrentRoot}
                handleSessionChipClick={handleSessionChipClick}
                handleSwitchWorktreeStart={handleSwitchWorktreeStart}
                handleTreeUpload={handleTreeUpload}
                handleTrustPendingPlugins={handleTrustPendingPlugins}
                handleViewerSelectionChange={handleViewerSelectionChange}
                kanbanTaskPanel={kanbanTaskPanel}
                mainDirectoryError={mainDirectoryError}
                mainView={mainView}
                managedRootByIdRef={managedRootByIdRef}
                matchedPlugin={matchedPlugin}
                persistFileScrollPositions={persistFileScrollPositions}
                pluginBypass={pluginBypass}
                pluginContentRef={pluginContentRef}
                pluginHandlers={pluginHandlers}
                pluginLoading={pluginLoading}
                pluginRender={pluginRender}
                pluginRendererKey={pluginRendererKey}
                pluginThemeVars={pluginThemeVars}
                projectAddMode={projectAddMode}
                projectAddOverlay={projectAddOverlay}
                refreshGitStatus={refreshGitStatus}
                refreshTreeDir={refreshTreeDir}
                selectedDir={selectedDir}
                selectedDirRef={selectedDirRef}
                selectedSession={selectedSession}
                setDirectorySortOverrides={setDirectorySortOverrides}
                setEntriesByPath={setEntriesByPath}
                setFile={setFile}
                setPluginBypass={setPluginBypass}
                setScheduledAgentDialogOpen={setScheduledAgentDialogOpen}
                showHiddenFiles={showHiddenFiles}
                switchToPluginView={switchToPluginView}
                switchToRawFileView={switchToRawFileView}
                t={t}
                visibleMainEntries={visibleMainEntries}
                workspaceBoard={workspaceBoard}
                workspaceOpen={workspaceOpen}
                worktreeCreateOverlay={worktreeCreateOverlay}
                worktreeSwitchOpen={worktreeSwitchOpen}
                worktreeSwitchOverlay={worktreeSwitchOverlay}
        />}
footer={<
        AppActionBar
        actionBarInputHistory={actionBarInputHistory}
        actionBarQueuedMessages={actionBarQueuedMessages}
        actionBarSession={actionBarSession}
        actionHandlers={actionHandlers}
        agentsVersion={agentsVersion}
        attachedFileContext={attachedFileContext}
        canOpenSessionDrawer={canOpenSessionDrawer}
        codexRateLimitsRefreshToken={codexRateLimitsRefreshToken}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentRootSlashCommandResult={currentRootSlashCommandResult}
        detachedBoundSession={detachedBoundSession}
        drawerOpenByRootRef={drawerOpenByRootRef}
        editDraftRequest={editDraftRequest}
        getDisplayNodeColor={getDisplayNodeColor}
        handleCancelCurrentTurn={handleCancelCurrentTurn}
        handleClearFileContext={handleClearFileContext}
        handleNewSession={handleNewSession}
        handleRemoveQueuedMessage={handleRemoveQueuedMessage}
        handleRequestFileContext={handleRequestFileContext}
        handleSelectSession={handleSelectSession}
        handleSendMessage={handleSendMessage}
        handleSendQueuedMessageNow={handleSendQueuedMessageNow}
        handleSetPlanMode={handleSetPlanMode}
        handleUpdateQueuedMessage={handleUpdateQueuedMessage}
        interactionModeRef={interactionModeRef}
        isDrawerOpen={isDrawerOpen}
        isMobile={isMobile}
        mainView={mainView}
        managedRootByIdRef={managedRootByIdRef}
        mobileEnterKeySends={mobileEnterKeySends}
        normalizeMode={normalizeMode}
        pendingPlanMode={pendingPlanMode}
        renderRootSlashCommandResult={renderRootSlashCommandResult({copiedSlashCommandKeys, copyText, reportError, setCopiedSlashCommandKeys, slashCopyResetTimersRef, t})}
        scopedRootKey={scopedRootKey}
        sendShortcut={sendShortcut}
        setDrawerOpenForRoot={setDrawerOpenForRoot}
        setInteractionMode={setInteractionMode}
        status={status}
        />}
drawer={<
        SessionSheet
        BottomSheet={BottomSheet}
        actionHandlers={actionHandlers}
        availableAgents={availableAgents}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentSession={currentSession}
        drawerLoadingSessionByRoot={drawerLoadingSessionByRoot}
        drawerScrollRef={drawerScrollRef}
        drawerSessionSnapshot={drawerSessionSnapshot}
        getDisplayNodeColor={getDisplayNodeColor}
        gitFileStatsByPath={gitFileStatsByPath}
        handleAskUserAnswer={handleAskUserAnswer}
        handleDrawerSessionFileClick={handleDrawerSessionFileClick}
        handleEditUserMessage={handleEditUserMessage}
        handleForkAgentMessage={handleForkAgentMessage}
        handleRemoveSessionRelatedFile={handleRemoveSessionRelatedFile}
        handleSelectSession={handleSelectSession}
        interactionModeRef={interactionModeRef}
        isDrawerOpen={isDrawerOpen}
        mainView={mainView}
        managedRootByIdRef={managedRootByIdRef}
        selectedKanbanTaskId={selectedKanbanTaskId}
        sessionViewerComposerOverlayInset={sessionViewerComposerOverlayInset}
        setDrawerOpenForRoot={setDrawerOpenForRoot}
        setInteractionMode={setInteractionMode}
        slashCommandResultForSession={slashCommandResultForSession}
        t={t}
        />}
      />
<OnboardingGate
      completeOnboarding={completeOnboarding}
      dismissOnboarding={dismissOnboarding}
      handleOnboardingStepChange={handleOnboardingStepChange}
      isMobile={isMobile}
      onboardingOpen={onboardingOpen}
      setIsLeftOpen={setIsLeftOpen}
      setIsRightOpen={setIsRightOpen}
      setOnboardingMainContentViewRoot={setOnboardingMainContentViewRoot}
      setOnboardingOpen={setOnboardingOpen}
      />
<E2eePrompt
      bootstrapState={bootstrapState}
      e2eePromptBusy={e2eePromptBusy}
      e2eePromptError={e2eePromptError}
      e2eeSecretInput={e2eeSecretInput}
      e2eeState={e2eeState}
      setE2eePromptError={setE2eePromptError}
      setE2eeSecretInput={setE2eeSecretInput}
      submitE2EESecret={submitE2EESecret}
      t={t}
      />
<TaskInlineEditPanel
      DEFAULT_TASK_AGENT={DEFAULT_TASK_AGENT}
      DEFAULT_TASK_MODEL={DEFAULT_TASK_MODEL}
      PanelShell={PanelShell}
      applyTaskInlineCandidate={applyTaskInlineCandidate}
      availableAgents={availableAgents}
      closeTaskEditDialog={closeTaskEditDialog}
      composerInputStyle={composerInputStyle}
      currentRootId={currentRootId}
      firstAgentStage={firstAgentStage}
      firstUserInputTemplate={firstUserInputTemplate}
      handleTaskInlineAttachmentChange={handleTaskInlineAttachmentChange}
      isMobile={isMobile}
      managedRootByIdRef={managedRootByIdRef}
      removeTaskInlineAttachment={removeTaskInlineAttachment}
      saveTaskInlineEdit={saveTaskInlineEdit}
      setTaskInlineActiveToken={setTaskInlineActiveToken}
      setTaskInlineCandidateIndex={setTaskInlineCandidateIndex}
      setTaskInlineCandidates={setTaskInlineCandidates}
      setTaskInlineEdit={setTaskInlineEdit}
      setTaskInlineHelpKey={setTaskInlineHelpKey}
      switchTaskInlineEditProject={switchTaskInlineEditProject}
      t={t}
      taskCreateProjectOptions={taskCreateProjectOptions}
      taskInlineActiveToken={taskInlineActiveToken}
      taskInlineAttachmentInputRef={taskInlineAttachmentInputRef}
      taskInlineCandidateIndex={taskInlineCandidateIndex}
      taskInlineCandidateItemRefs={taskInlineCandidateItemRefs}
      taskInlineCandidates={taskInlineCandidates}
      taskInlineEdit={taskInlineEdit}
      taskInlineEditorRef={taskInlineEditorRef}
      taskInlineHelpKey={taskInlineHelpKey}
      taskInlineSaving={taskInlineSaving}
      taskInlineUploadAbortRef={taskInlineUploadAbortRef}
      taskInlineUploadProgress={taskInlineUploadProgress}
      taskTemplates={taskTemplates}
      taskWorktreeBranches={taskWorktreeBranches}
      />
<TaskSessionErrorDialog
      setTaskSessionErrorDialog={setTaskSessionErrorDialog}
      t={t}
      taskCardIconButtonStyle={taskCardIconButtonStyle}
      taskSessionErrorDialog={taskSessionErrorDialog}
      />
      <ScheduledAgentTaskDialog
        open={scheduledAgentDialogOpen}
        rootId={currentRootId}
        agents={availableAgents}
        onClose={() => setScheduledAgentDialogOpen(false)}
      />
      <TaskTemplateDialog
        open={taskTemplateDialogOpen}
        agents={availableAgents}
        template={taskTemplateDialogTemplate}
        onClose={() => setTaskTemplateDialogOpen(false)}
        onSaved={handleTaskTemplateSaved}
        currentRootId={currentRootId}
      />
<TaskDetailPanelGate
      applyTaskDetails={applyTaskDetails}
      availableAgents={availableAgents}
      currentRootId={currentRootId}
      currentRootIdRef={currentRootIdRef}
      getDisplayNodeColor={getDisplayNodeColor}
      getNodeIdForRoot={getNodeIdForRoot}
      handleMoveKanbanTask={handleMoveKanbanTask}
      handleTaskSessionDrawerOpen={handleTaskSessionDrawerOpen}
      selectedKanbanTask={selectedKanbanTask}
      setSelectedKanbanTaskId={setSelectedKanbanTaskId}
      taskDetailsById={taskDetailsById}
      />
      <ToastContainer />
      <DialogHost />
    </>
  );
}
