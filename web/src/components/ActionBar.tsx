import React, { useCallback, useEffect, useRef, useState } from "react";
import { ModeSelector, type SessionMode } from "./ModeSelector";
import { AgentSelector } from "./AgentSelector";
import { fetchAgents, fetchShells, restartAgent, type AgentStatus } from "../services/agents";
import { getRootNodeId } from "../services/rootNode";
import { type CandidateItem } from "../services/candidates";
import { reportError } from "../services/error";
import { isUploadAbortError, uploadFiles, type UploadProgress } from "../services/upload";
import TokenEditor, { type TokenEditorHandle } from "./editor/TokenEditor";
import { useI18n, type MessageKey } from "../i18n";
import {
  CandidateDropdown,
} from "./action/CandidateDropdown";
import {
  AttachmentsArea,
  type PendingAttachment,
} from "./action/AttachmentsArea";
import { QueuedMessagesList } from "./action/QueuedMessagesList";
import { ShellSelector } from "./action/ShellSelector";
import { ComposerTopChips } from "./action/ComposerTopChips";
import { SessionRing } from "./action/SessionRing";
import {
  appAccentHexToRgba,
  chatBlurPlaceholderKeys,
  IME_ENTER_GUARD_MS,
  modePlaceholderKeys,
  useResponsive,
  wsStatusMeta,
} from "./action/styleHelpers";
import { useAppearanceSync } from "./action/useAppearanceSync";
import { useCandidates } from "./action/useCandidates";
import { useDisplayStatus } from "./action/useDisplayStatus";
import { useInputHistory } from "./action/useInputHistory";
import { usePendingAttachments } from "./action/usePendingAttachments";
import { useWorktreeState } from "./action/useWorktreeState";
import {
  getAgentDefaults,
  has1MSuffix,
  isClaudeAgentName,
  modelBaseForAgent,
  with1MSuffix,
} from "./action/modelUtils";
import { type ActionBarProps, type AttachedFileContext, type QueuedMessageInfo, type SessionInfo, type WSStatus } from "./action/types";
import { replaceActiveTokenText, parsePlanCommand, stripPlanCommandPrefix } from "./action/inputTransforms";
import { matchesSendShortcut } from "../services/sendShortcut";
import { type ShellStatus } from "../services/agents";

export type {
  ActionBarProps,
  AttachedFileContext,
  QueuedMessageInfo,
  SessionInfo,
  WSStatus,
} from "./action/types";

function wsStatusDisplayDelay(status: WSStatus): number {
  return status === "reconnecting" ? 800 : 0;
}

export function ActionBar({
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
  onRequestFileContext,
  onClearFileContext,
  onSessionClick,
  onToggleLeftSidebar,
  onToggleRightSidebar,
  mobileEnterKeySends = false,
  sendShortcut = null,
  sidebarsSwapped = false,
}: ActionBarProps) {
  const { t } = useI18n();
  const [mode, setMode] = useState<SessionMode>("chat");
  const [agent, setAgent] = useState("");
  const [model, setModel] = useState("");
  const [agentMode, setAgentMode] = useState("");
  const [effort, setEffort] = useState("");
  const [fastService, setFastService] = useState<"" | "on" | "off">("");
  const [longContext, setLongContext] = useState(false);
  const [agents, setAgents] = useState<AgentStatus[]>([]);
  const [shells, setShells] = useState<ShellStatus[]>([]);
  const [shell, setShell] = useState("");
  const [serializedInput, setSerializedInput] = useState("");
  const [activeToken, setActiveToken] = useState<{ type: "file" | "slash" | "prompt" | "command"; query: string } | null>(null);
  const [sending, setSending] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [isMultiLine, setIsMultiLine] = useState(false);
  const [isFocused, setIsFocused] = useState(false);
  const [blurPlaceholderKey, setBlurPlaceholderKey] = useState<MessageKey>(
    () => chatBlurPlaceholderKeys[Math.floor(Math.random() * chatBlurPlaceholderKeys.length)] || "action.placeholder.chat",
  );
  const [agentSelectorOpen, setAgentSelectorOpen] = useState(false);
  const [modeSelectorOpen, setModeSelectorOpen] = useState(false);
  const syncedSessionSignatureRef = useRef<string>("");
  const editorRef = useRef<TokenEditorHandle>(null);
  const suppressedCommandCandidateTextRef = useRef("");
  const attachmentInputRef = useRef<HTMLInputElement>(null);
  const uploadAbortRef = useRef<AbortController | null>(null);
  const isComposingRef = useRef(false);
  const compositionGuardUntilRef = useRef(0);
  const { isMobile } = useResponsive();
  const isDark = useAppearanceSync();
  const clearActiveToken = useCallback(() => setActiveToken(null), []);
  const {
    candidates,
    setCandidates,
    activeCandidateIndex,
    setActiveCandidateIndex,
    candidateItemRefs,
    bumpPromptCandidateRefresh,
  } = useCandidates({ activeToken, currentRootId, agent, mode, isFocused, clearActiveToken });
  const clearCandidates = useCallback(() => {
    setCandidates([]);
    setActiveCandidateIndex(0);
  }, []);
  const {
    pendingAttachments,
    appendPendingAttachments,
    removePendingAttachment,
    clearPendingAttachments,
  } = usePendingAttachments();
  const {
    createWorktree,
    setCreateWorktree,
    worktreeBranchMode,
    setWorktreeBranchMode,
    worktreeBranch,
    setWorktreeBranch,
    worktreeBranches,
    worktreeBranchesLoading,
    worktreeBranchError,
  } = useWorktreeState({ currentRootId, currentRootIsGitRepo, currentSession, mode });
  const [ringDragging, setRingDragging] = useState(false);
  const isConnected = status === "connected";
  const displayStatus = useDisplayStatus(status);
  const connectionMeta = wsStatusMeta(displayStatus, t);
  const accentColorRaw = String(rootColor || "").trim() || "var(--accent-color)";
  const accentHex = String(rootColor || "").trim() || "#2563eb";

  useEffect(() => {
    const sessionKey = currentSession?.key || currentSession?.session_key || null;
    if (!currentSession) {
      syncedSessionSignatureRef.current = "";
      return;
    }
    const nextMode = currentSession.type === "plugin" ? "plugin" : currentSession.type === "command" ? "command" : "chat";
    const nextAgent = currentSession.agent || "";
    const nextModel = currentSession.model || "";
    const nextShell = currentSession.shell || "";
    const nextAgentMode = currentSession.mode || "";
    const nextEffort = currentSession.effort || "";
    const nextFastService = (currentSession.fast_service || "") as "" | "on" | "off";
    const nextLongContext = isClaudeAgentName(currentSession.agent) && has1MSuffix(currentSession.model || "");
    const signature = `${sessionKey || ""}::${nextMode}::${nextAgent}::${nextModel}::${nextShell}::${nextAgentMode}::${nextEffort}::${nextFastService}::${nextLongContext ? "1m" : ""}`;
    if (syncedSessionSignatureRef.current === signature) {
      return;
    }
    syncedSessionSignatureRef.current = signature;
    setMode(nextMode);
    setAgent(nextAgent);
    setModel(nextModel);
    setShell(nextShell);
    setAgentMode(nextAgentMode);
    setEffort(nextEffort);
    setFastService(nextFastService);
    setLongContext(nextLongContext);
  }, [currentSession]);

  useEffect(() => {
    if (!currentSession?.pending) {
      setCancelling(false);
    }
  }, [currentSession?.key, currentSession?.session_key, currentSession?.pending]);

  useEffect(() => {
    const nid = getRootNodeId(currentRootId || "") as any;
    Promise.all([fetchAgents(true, nid), fetchShells(true, nid)])
      .then(([nextAgents, nextShells]) => {
        setAgents(nextAgents);
        setShells(nextShells);
      })
      .catch((err) => console.error("Failed to fetch agents:", err));
  }, [agentsVersion, currentRootId]);

  useEffect(() => {
    if (mode !== "command" || shells.length === 0) {
      return;
    }
    if (shells.some((item) => item.id === shell || item.command === shell || item.resolved_command === shell)) {
      return;
    }
    const preferred = shells.find((item) => item.default) || shells[0];
    setShell(preferred?.id || "");
  }, [mode, shell, shells]);

  useEffect(() => {
    if (currentSession || agents.length === 0) return;
    if (agents.some((a) => a.name === agent)) return;
    const preferred = agents.find((a) => a.available) ?? agents[0];
    if (!preferred) {
      return;
    }
    const defaults = getAgentDefaults(preferred);
    setAgent(preferred.name);
    setModel(defaults.model);
    setAgentMode("");
    setEffort(defaults.effort);
    setFastService(defaults.fastService);
  }, [agent, agents, currentSession]);

  useEffect(() => {
    if (!agent || !model) {
      return;
    }
    const selectedAgent = agents.find((item) => item.name === agent);
    if (!selectedAgent) {
      return;
    }
    const hasModel = (selectedAgent.models ?? []).some(
      (item) => modelBaseForAgent(selectedAgent.name, item.id) === modelBaseForAgent(selectedAgent.name, model),
    );
    if (!hasModel) {
      setModel("");
      if (isClaudeAgentName(selectedAgent.name) && longContext) {
        setLongContext(false);
      }
    }
  }, [agent, longContext, model, agents]);

  const selectedAgent = agents.find((item) => item.name === agent);
  const selectedModelInfo =
    (selectedAgent?.models ?? []).find(
      (item) => modelBaseForAgent(selectedAgent?.name, item.id) === modelBaseForAgent(selectedAgent?.name, model),
    )
    || (selectedAgent?.models ?? []).find(
      (item) => modelBaseForAgent(selectedAgent?.name, item.id) === modelBaseForAgent(selectedAgent?.name, selectedAgent?.default_model_id || selectedAgent?.current_model_id || ""),
    );
  const availableEfforts = selectedModelInfo?.efforts ?? selectedAgent?.efforts ?? [];
  const isCodexEffortAgent = selectedAgent?.name === "codex";
  const supportsEffort =
    availableEfforts.length > 0 && !!selectedModelInfo?.supportEffort;
  const supportsServiceTier = !!selectedAgent?.supports_fast_service;
  const supportsLongContext = isClaudeAgentName(selectedAgent?.name);
  const effectiveModelForSend = supportsLongContext ? with1MSuffix(model, longContext) : model;
  const planModeActive = (!!currentSession?.plan_mode || pendingPlanMode) && mode !== "command";
  const planSessionKey = currentSession?.key || currentSession?.session_key || "";
  const planRootId = currentSession?.root_id || currentRootId || "";
  const sessionHistoryKey = currentSession?.key || currentSession?.session_key || "";

  useEffect(() => {
    if (!supportsEffort) {
      if (effort) {
        setEffort("");
      }
      return;
    }
    if (effort && !availableEfforts.includes(effort)) {
      setEffort(getAgentDefaults(selectedAgent).effort);
    }
  }, [supportsEffort, effort, availableEfforts, selectedAgent, isCodexEffortAgent]);

  useEffect(() => {
    if (!supportsServiceTier) {
      return;
    }
    if (fastService === "on" && !selectedAgent?.supports_fast_service) {
      setFastService(getAgentDefaults(selectedAgent).fastService);
    }
  }, [supportsServiceTier, fastService, selectedAgent]);

  useEffect(() => {
    if (!supportsLongContext && longContext) {
      setLongContext(false);
    }
  }, [supportsLongContext, longContext]);

  const syncEditorHeight = useCallback(() => {
    const height = editorRef.current?.getHeight() || 44;
    setIsMultiLine(height > 50);
  }, []);

  const {
    inputHistoryIndex,
    setInputHistoryIndex,
    applyingInputHistoryRef,
    inputHistoryDraftRef,
    navigateInputHistory,
    resetHistoryCursor,
  } = useInputHistory({
    editorRef,
    serializedInput,
    inputHistory,
    sessionHistoryKey,
    syncEditorHeight,
    clearActiveToken,
    clearCandidates,
    setSerializedInput,
  });

  useEffect(() => {
    if (!editDraftRequest) {
      return;
    }
    const nextText = editDraftRequest.content || "";
    editorRef.current?.setText(nextText);
    setSerializedInput(nextText);
    suppressedCommandCandidateTextRef.current = "";
    setActiveToken(null);
    setCandidates([]);
    setActiveCandidateIndex(0);
    requestAnimationFrame(syncEditorHeight);
  }, [editDraftRequest, syncEditorHeight]);

  const handleEditorChange = useCallback((payload: {
    serializedText: string;
    displayText: string;
    activeToken: { type: "file" | "slash" | "prompt" | "command"; query: string } | null;
  }) => {
    setSerializedInput(payload.serializedText);
    if (!applyingInputHistoryRef.current && inputHistoryIndex !== null) {
      setInputHistoryIndex(null);
      inputHistoryDraftRef.current = payload.serializedText;
    }
    if (mode === "command") {
      const query = payload.displayText.trim();
      if (!query) {
        suppressedCommandCandidateTextRef.current = "";
        setActiveToken(null);
      } else if (payload.activeToken) {
        suppressedCommandCandidateTextRef.current = "";
        setActiveToken(payload.activeToken);
      } else if (query === suppressedCommandCandidateTextRef.current) {
        setActiveToken(null);
      } else {
        suppressedCommandCandidateTextRef.current = "";
        setActiveToken({ type: "command", query });
      }
    } else {
      suppressedCommandCandidateTextRef.current = "";
      setActiveToken(payload.activeToken);
    }
    if (payload.displayText.trim().length === 0) {
      setIsMultiLine(false);
      return;
    }
    requestAnimationFrame(syncEditorHeight);
  }, [inputHistoryIndex, mode, syncEditorHeight]);

  const applyCandidate = useCallback((candidate: CandidateItem) => {
    if (!activeToken) return;
    setCandidates([]);
    setActiveCandidateIndex(0);
    if (candidate.type === "command") {
      suppressedCommandCandidateTextRef.current = candidate.name.trim();
      editorRef.current?.setText(candidate.name);
    } else if (mode === "command" && candidate.type === "file") {
      suppressedCommandCandidateTextRef.current = "";
      const nextText = replaceActiveTokenText(serializedInput, activeToken, candidate.name);
      editorRef.current?.setText(nextText);
      setSerializedInput(nextText);
    } else {
      suppressedCommandCandidateTextRef.current = "";
      editorRef.current?.insertCandidate(candidate.type, candidate.name);
    }
    editorRef.current?.focus();
    syncEditorHeight();
  }, [activeToken, mode, serializedInput, syncEditorHeight]);

  const removePromptLocally = useCallback((text: string) => {
    setCandidates((items) => items.filter((item) => item.type !== "prompt" || item.name !== text));
    setActiveCandidateIndex(0);
    bumpPromptCandidateRefresh();
  }, []);

  const handleSend = useCallback(async () => {
    const messageText = serializedInput.trim();
    if ((!messageText && pendingAttachments.length === 0) || !isConnected || sending || (mode !== "command" && !agent)) return;
    const planCommand = pendingAttachments.length === 0 ? parsePlanCommand(messageText) : null;
    if (planCommand !== null) {
      const planContent = stripPlanCommandPrefix(messageText);
      if (!planContent) {
        setSending(true);
        try {
          await onSetPlanMode?.(planCommand, planSessionKey, planRootId);
          editorRef.current?.clear();
          setSerializedInput("");
          setActiveToken(null);
          setCandidates([]);
          setActiveCandidateIndex(0);
        } finally {
          setSending(false);
          if (!isMobile) {
            requestAnimationFrame(() => editorRef.current?.focus());
          }
        }
        return;
      }
    }
    setSending(true);
    setUploadProgress(null);
    setCandidates([]);
    setActiveCandidateIndex(0);
    try {
      let attachmentTokens = "";
      if (pendingAttachments.length > 0) {
        if (!currentRootId) {
          reportError("file.write_failed", t("action.uploadNoProject"));
          return;
        }
        const uploadAbort = new AbortController();
        uploadAbortRef.current = uploadAbort;
        const uploaded = await uploadFiles({
          rootId: currentRootId,
          files: pendingAttachments.map((attachment) => attachment.file),
          onProgress: setUploadProgress,
          signal: uploadAbort.signal,
        });
        attachmentTokens = uploaded
          .map((file) => `[file: ${file.agent_path || file.path}]`)
          .join("\n");
      }
      const payload = [messageText, attachmentTokens].filter(Boolean).join("\n");
      if (!payload) {
        return;
      }
      await onSendMessage?.(
        payload,
        mode,
        mode === "command" ? "" : agent,
        effectiveModelForSend || undefined,
        agentMode || undefined,
        supportsEffort ? effort || undefined : undefined,
        supportsServiceTier ? fastService : undefined,
        mode === "command" ? shell || undefined : undefined,
        !currentSession && mode !== "command" && currentRootIsGitRepo
          ? {
              create: createWorktree,
              branchMode: worktreeBranchMode,
              branch: worktreeBranchMode === "existing" ? worktreeBranch : "",
            }
          : undefined,
      );
      editorRef.current?.clear();
      setSerializedInput("");
      setInputHistoryIndex(null);
      inputHistoryDraftRef.current = "";
      setActiveToken(null);
      setCandidates([]);
      setActiveCandidateIndex(0);
      clearPendingAttachments();
      setIsMultiLine(false);
      setCreateWorktree(false);
      setWorktreeBranchMode("new");
      setWorktreeBranch("");
      if (isMobile) {
        requestAnimationFrame(() => editorRef.current?.blur());
      }
    } catch (err) {
      if (!isUploadAbortError(err)) {
        reportError("file.write_failed", String((err as Error)?.message || t("action.uploadFailed")));
      }
    } finally {
      uploadAbortRef.current = null;
      setSending(false);
      setUploadProgress(null);
      if (!isMobile) {
        requestAnimationFrame(() => editorRef.current?.focus());
      }
    }
  }, [serializedInput, pendingAttachments, isConnected, sending, mode, agent, currentRootId, planSessionKey, planRootId, onSetPlanMode, isMobile, effectiveModelForSend, agentMode, onSendMessage, supportsEffort, effort, supportsServiceTier, fastService, shell, t, currentSession, currentRootIsGitRepo, createWorktree, worktreeBranchMode, worktreeBranch]);

  const handleCancel = useCallback(async () => {
    const sessionKey = currentSession?.key;
    if (!sessionKey || cancelling) return;
    setCancelling(true);
    try {
      await onCancelCurrentTurn?.(sessionKey);
    } finally {
      setCancelling(false);
    }
  }, [currentSession?.key, cancelling, onCancelCurrentTurn]);

  const isCompositionActive = useCallback((event?: KeyboardEvent | null) => {
    const nativeEvent = event as (KeyboardEvent & { isComposing?: boolean; keyCode?: number }) | null | undefined;
    return isComposingRef.current
      || performance.now() < compositionGuardUntilRef.current
      || !!nativeEvent?.isComposing
      || nativeEvent?.keyCode === 229;
  }, []);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (isCompositionActive(e.nativeEvent)) {
      return;
    }
    if (e.key !== "Enter" && !e.repeat && matchesSendShortcut(e.nativeEvent, sendShortcut)) {
      e.preventDefault();
      e.stopPropagation();
      void handleSend();
      return;
    }
    if (candidates.length > 0) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveCandidateIndex((prev) => (prev < 0 ? 0 : (prev + 1) % candidates.length));
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveCandidateIndex((prev) => (prev < 0 ? candidates.length - 1 : (prev - 1 + candidates.length) % candidates.length));
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        if (activeCandidateIndex >= 0) {
          applyCandidate(candidates[activeCandidateIndex]);
        }
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setCandidates([]);
        setActiveCandidateIndex(0);
        return;
      }
    }
    if (!activeToken && !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
      if (e.key === "ArrowUp" && navigateInputHistory("previous")) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      if (e.key === "ArrowDown" && navigateInputHistory("next")) {
        e.preventDefault();
        e.stopPropagation();
      }
    }
  }, [candidates, activeCandidateIndex, applyCandidate, handleSend, isCompositionActive, activeToken, navigateInputHistory, sendShortcut]);

  const handleEditorEnter = useCallback((event: KeyboardEvent | null) => {
    if (isCompositionActive(event)) {
      // Stop Lexical's plain-text Enter handler without preventing the native
      // event, so an IME can finish committing its composition text.
      return true;
    }
    if (event && !event.repeat && matchesSendShortcut(event, sendShortcut)) {
      event.preventDefault();
      event.stopPropagation();
      void handleSend();
      return true;
    }
    if (event?.shiftKey) {
      return false;
    }
    if (candidates.length > 0) {
      if (activeCandidateIndex >= 0) {
        event?.preventDefault();
        event?.stopPropagation();
        applyCandidate(candidates[activeCandidateIndex]);
        return true;
      }
      if (mode !== "command") {
        event?.preventDefault();
        event?.stopPropagation();
        applyCandidate(candidates[0]);
        return true;
      }
    }
    if (!isMobile || (mobileEnterKeySends && mode === "chat")) {
      event?.preventDefault();
      event?.stopPropagation();
      void handleSend();
      return true;
    }
    return false;
  }, [candidates, activeCandidateIndex, applyCandidate, handleSend, isCompositionActive, isMobile, mobileEnterKeySends, mode, sendShortcut]);

  const handleEditorPaste = useCallback((event: React.ClipboardEvent<HTMLDivElement>) => {
    if (sending || !currentRootId) {
      return;
    }
    const clipboardItems = Array.from(event.clipboardData?.items || []);
    const imageFiles = clipboardItems
      .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
      .map((item) => item.getAsFile())
      .filter((file): file is File => !!file);
    if (imageFiles.length === 0) {
      return;
    }
    event.preventDefault();
    appendPendingAttachments(imageFiles);
  }, [appendPendingAttachments, currentRootId, sending]);

  const resetForNewSession = useCallback(() => {
    const nextAgent = agents.find((item) => item.name === agent)
      || agents.find((item) => item.available)
      || agents[0];
    if (!nextAgent) {
      return;
    }
    const defaults = getAgentDefaults(nextAgent);
    setAgent(nextAgent.name);
    setModel(defaults.model);
    setAgentMode("");
    setEffort(defaults.effort);
    setFastService(defaults.fastService);
    setLongContext(has1MSuffix(defaults.model));
    syncedSessionSignatureRef.current = "";
  }, [agent, agents]);

  const isSelectedAgentUnavailable = agents.length > 0 ? agents.find((a) => a.name === agent)?.available === false : false;
  const canSend = (!!serializedInput.trim() || pendingAttachments.length > 0) && isConnected && !sending && (mode === "command" || !!agent);
  const hasBoundSession = !!currentSession;
  const hasDraft = !!serializedInput.trim() || pendingAttachments.length > 0;
  const showCancel = !!currentSession?.pending && !!currentSession?.key && !hasDraft;
  const isModeLocked = !!currentSession;

  useEffect(() => {
    if (isMobile || !showCancel) {
      return;
    }
    const cancelOnEscape = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.key !== "Escape" || isCompositionActive(event) || event.repeat) {
        return;
      }
      event.preventDefault();
      void handleCancel();
    };
    window.addEventListener("keydown", cancelOnEscape);
    return () => window.removeEventListener("keydown", cancelOnEscape);
  }, [handleCancel, isCompositionActive, isMobile, showCancel]);

  const inputPlaceholder = currentSession && !currentSession.pending
    ? t("action.placeholder.newSessionSwipe")
    : mode === "chat" && !isFocused
      ? t(blurPlaceholderKey)
      : t(modePlaceholderKeys[mode]);
  const editorRightInset = isMultiLine ? 14 : mode === "command" ? (isMobile ? 92 : 116) : isMobile ? 124 : 148;
  const editorBottomInset = isMultiLine ? 44 : 12;
  const editorMinHeight = 44;
  const mobileFileSidebarButton = isMobile ? (
    <button
      type="button"
      onClick={onToggleLeftSidebar}
      style={{ width: "30px", height: "44px", borderRadius: "0", border: "none", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer", opacity: 0.86, outline: "none", boxShadow: "none", WebkitTapHighlightColor: "transparent" as any, overflow: "hidden" }}
      aria-label={t("sidebar.openFile")}
      title={t("sidebar.file")}
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none">
        <path fill="currentColor" d="M3 3h6v4H3zm12 7h6v4h-6zm0 7h6v4h-6zm-2-4H7v5h6v2H5V9h2v2h6z" style={{ transform: "scale(1.28)", transformOrigin: "12px 12px" }} />
      </svg>
    </button>
  ) : null;
  const mobileSessionSidebarButton = isMobile ? (
    <button
      type="button"
      onClick={onToggleRightSidebar}
      style={{ width: "30px", height: "44px", borderRadius: "0", border: "none", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer", opacity: 0.86, outline: "none", boxShadow: "none", WebkitTapHighlightColor: "transparent" as any, overflow: "hidden" }}
      aria-label={t("sidebar.openSession")}
      title={t("sidebar.session")}
    >
      <svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3.8" strokeLinecap="round">
        <line x1="6" y1="4" x2="18" y2="4" />
        <line x1="6" y1="12" x2="18" y2="12" />
        <line x1="6" y1="20" x2="18" y2="20" />
      </svg>
    </button>
  ) : null;

  // 看板/工作台：不渲染输入区。移动端仍要保留呼出左右侧栏的按钮，否则进了这两个界面就再也开不出侧栏。
  if (hideComposer) {
    return isMobile ? (
      <div
        data-onboarding="action-bar"
        style={{
          width: "100%",
          minWidth: 0,
          padding: "0 0 var(--mindfs-actionbar-bottom-padding, calc(env(safe-area-inset-bottom, 0px) + 2px))",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          boxSizing: "border-box",
          background: "var(--content-bg)",
        }}
      >
        {sidebarsSwapped ? mobileSessionSidebarButton : mobileFileSidebarButton}
        {sidebarsSwapped ? mobileFileSidebarButton : mobileSessionSidebarButton}
      </div>
    ) : null;
  }



  return (
    <div data-onboarding="action-bar" style={{ width: "100%", minWidth: 0, padding: isMobile ? "0 0 var(--mindfs-actionbar-bottom-padding, calc(env(safe-area-inset-bottom, 0px) + 2px))" : "0 16px 12px", display: "flex", justifyContent: "center", boxSizing: "border-box", background: "var(--content-bg)" }}>
      <div style={{ position: "relative", width: "100%", minWidth: 0, display: "flex", flexDirection: "column", gap: 0 }}>
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
        <div style={{ display: "grid", gridTemplateColumns: isMobile ? "30px minmax(0, 1fr) 30px" : "1fr", alignItems: isMobile ? "end" : "center", gap: isMobile ? "1px" : 0, padding: isMobile ? "0 1px" : 0, minWidth: 0, maxWidth: "100%" }}>
          {sidebarsSwapped ? mobileSessionSidebarButton : mobileFileSidebarButton}

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
              {(planModeActive || (!currentSession && currentRootIsGitRepo && mode !== "command") || (mode !== "command" && agent === "codex")) ? (
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
              ) : null}
              <TokenEditor
                ref={editorRef}
                placeholder={inputPlaceholder}
                disabled={sending}
                isDark={isDark}
                rightInset={editorRightInset}
                topInset={0}
                bottomInset={editorBottomInset}
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

              <div data-onboarding="input-controls" style={{ position: "absolute", right: isMobile ? "4px" : "8px", bottom: isMultiLine ? "6px" : "50%", transform: isMultiLine ? "none" : "translateY(50%)", display: "flex", alignItems: "center", gap: isMobile ? "0px" : "2px", zIndex: 5, transition: "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)" }}>
                <SessionRing
                  hasBoundSession={hasBoundSession}
                  canOpenSessionDrawer={canOpenSessionDrawer}
                  sessionDrawerOpen={sessionDrawerOpen}
                  detachedBoundSession={detachedBoundSession}
                  accentColorRaw={accentColorRaw}
                  accentHex={accentHex}
                  currentSession={currentSession ?? null}
                  onSessionClick={onSessionClick ?? (() => {})}
                  onDraggingChange={setRingDragging}
                  onNewSession={onNewSession}
                  onResetForNewSession={resetForNewSession}
                />

                <>
                  <ModeSelector mode={mode} onModeChange={setMode} compact={true} disabled={isModeLocked} onboardingId="mode-selector" viewportMenu accentColor={accentHex} onOpenChange={setModeSelectorOpen} />
                  {mode !== "command" ? (
                    <div>
                      <AgentSelector
                      agent={agent}
                      model={model}
                      mode={agentMode}
                      effort={effort}
                      longContext={longContext}
                      agents={agents}
                      onAgentChange={(nextAgent, nextModel) => {
                        const nextStatus = agents.find((item) => item.name === nextAgent);
                        const defaults = getAgentDefaults(nextStatus);
                        const explicitModel = String(nextModel || "").trim();
                        setAgent(nextAgent);
                        setModel(explicitModel || defaults.model);
                        setAgentMode("");
                        setEffort(defaults.effort);
                        setFastService(defaults.fastService);
                        if (explicitModel) {
                          setLongContext(isClaudeAgentName(nextAgent) && has1MSuffix(explicitModel));
                        } else {
                          setLongContext(isClaudeAgentName(nextAgent) && has1MSuffix(defaults.model));
                        }
                      }}
                      onModeChange={(nextAgentMode) => setAgentMode(nextAgentMode || "")}
                      onEffortChange={(nextEffort) => setEffort(nextEffort || "")}
                      onLongContextChange={(next) => setLongContext(!!next)}
                      fastService={fastService}
                      onFastServiceChange={(nextFastService) => setFastService(nextFastService || "")}
                      onAgentRestart={async (targetAgent) => {
                        const nid = getRootNodeId(currentRootId || "") as any;
                        await restartAgent(targetAgent, nid);
                        const items = await fetchAgents(true, nid);
                        setAgents(items);
                      }}
                      compact={true}
                      warnUnavailable={isSelectedAgentUnavailable}
                      defaultExpandOptions
                      onboardingId="agent-selector"
                      viewportMenu
                      onOpenChange={setAgentSelectorOpen}
                      />
                    </div>
                  ) : (
                    <ShellSelector
                      shell={shell}
                      shells={shells}
                      onShellChange={setShell}
                      compact={true}
                    />
                  )}
                </>

                <>
                  <button
                  data-onboarding="attachment-action"
                  type="button"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => attachmentInputRef.current?.click()}
                  disabled={!currentRootId || sending}
                  style={{
                    width: "28px",
                    height: "28px",
                    borderRadius: "8px",
                    border: "none",
                    background: pendingAttachments.length > 0
                      ? appAccentHexToRgba(accentHex, 0.14)
                      : "transparent",
                    color: pendingAttachments.length > 0
                      ? accentColorRaw
                      : "var(--text-secondary)",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                    cursor: !currentRootId || sending ? "not-allowed" : "pointer",
                    opacity: !currentRootId || sending ? 0.35 : 1,
                  }}
                  title={t("action.addAttachment")}
                  aria-label={t("action.addAttachment")}
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
                    <path d="M12 5v14" />
                    <path d="M5 12h14" />
                  </svg>
                  </button>
                  <button
                  data-onboarding="send-action"
                  type="button"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={showCancel ? handleCancel : handleSend}
                  disabled={showCancel ? cancelling : !canSend}
                  style={{ width: "28px", height: "28px", borderRadius: "8px", border: "none", background: showCancel ? "rgba(239,68,68,0.14)" : (canSend ? accentColorRaw : "transparent"), color: showCancel ? "#ef4444" : (canSend ? "#fff" : "var(--text-secondary)"), display: "flex", alignItems: "center", justifyContent: "center", cursor: showCancel ? (cancelling ? "wait" : "pointer") : (canSend ? "pointer" : "not-allowed"), transition: "all 0.2s", opacity: showCancel ? 1 : (canSend ? 1 : 0.3) }}
                >
                  {sending || cancelling ? (
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" style={{ animation: "spin 1s linear infinite" }}><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
                  ) : showCancel ? (
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2.5" /></svg>
                  ) : (
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>
                  )}
                  </button>
                </>
                <input
                  ref={attachmentInputRef}
                  type="file"
                  multiple
                  style={{ display: "none" }}
                  onChange={(event) => {
                    const selectedFiles = Array.from(event.target.files || []);
                    if (selectedFiles.length > 0) {
                      appendPendingAttachments(selectedFiles);
                    }
                    event.currentTarget.value = "";
                  }}
                />
              </div>
            </div>
          </div>

          {sidebarsSwapped ? mobileFileSidebarButton : mobileSessionSidebarButton}
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
