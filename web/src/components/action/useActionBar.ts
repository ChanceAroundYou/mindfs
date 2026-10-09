import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAgents, fetchShells, type AgentStatus } from "../../services/agents";
import { getRootNodeId } from "../../services/net/rootNode";
import { reportError } from "../../services/net/error";
import { fileTokenPath, formatFileToken, isUploadAbortError, uploadFiles, type UploadProgress } from "../../services/upload";
import type { TokenEditorHandle } from "../editor/TokenEditor";
import { useI18n, type MessageKey } from "../../i18n/index";
import {
  appAccentHexToRgba,
  chatBlurPlaceholderKeys,
  IME_ENTER_GUARD_MS,
  modePlaceholderKeys,
  useResponsive,
  wsStatusMeta,
} from "./styleHelpers";
import { useAppearanceSync } from "./useAppearanceSync";
import { DEFAULT_NODE_COLOR } from "../../services/net/nodeRegistry";
import { useCandidates } from "./useCandidates";
import { useDisplayStatus } from "./useDisplayStatus";
import { useInputHistory } from "./useInputHistory";
import { composerEditorInsets, useComposerEditorHeight } from "./useComposerEditorHeight";
import { usePendingAttachments } from "./usePendingAttachments";
import { useWorktreeState } from "./useWorktreeState";
import {
  getAgentDefaults,
  has1MSuffix,
  isClaudeAgentName,
  modelBaseForAgent,
  strip1MSuffix,
  with1MSuffix,
} from "./modelUtils";
import { type ActionBarProps } from "./types";
import { parsePlanCommand, replaceActiveTokenText, stripPlanCommandPrefix } from "./inputTransforms";
import { matchesSendShortcut } from "../../services/prefs/sendShortcut";
import type { ShellStatus } from "../../services/agents";
import type { CandidateItem } from "../../services/candidates";
import type { SessionMode } from "../agent/ModeSelector";
import type { PendingAttachment } from "./AttachmentsArea";

export function useActionBar(props: ActionBarProps) {
  const {
    status = "disconnected",
    agentsVersion = 0,
    codexRateLimitsRefreshToken = 0,
    currentRootId,
    currentRootIsGitRepo = false,
    currentSession,
    pendingPlanMode = false,
    rootColor = null,
    hideComposer = false,
    editDraftRequest = null,
    inputHistory = [],
    onSendMessage,
    onSetPlanMode,
    onCancelCurrentTurn,
    onRequestFileContext,
    mobileEnterKeySends = false,
    sendShortcut = null,
  } = props;

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
  const accentHex = String(rootColor || "").trim() || DEFAULT_NODE_COLOR;

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

  const {
    isMultiLine,
    syncEditorHeight,
    resetEditorHeight,
  } = useComposerEditorHeight(editorRef);

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
      resetEditorHeight();
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
          .map((file) => formatFileToken(fileTokenPath(file)))
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
      resetEditorHeight();
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
  const editorInsets = composerEditorInsets(
    isMultiLine,
    mode === "command" ? (isMobile ? 92 : 116) : isMobile ? 124 : 148,
  );
  const editorMinHeight = 44;

  return {
    mode, setMode,
    agent, setAgent,
    model, setModel,
    agentMode, setAgentMode,
    effort, setEffort,
    fastService, setFastService,
    longContext, setLongContext,
    agents, setAgents,
    shells, setShells,
    shell, setShell,
    serializedInput, setSerializedInput,
    activeToken, setActiveToken,
    sending, setSending,
    uploadProgress, setUploadProgress,
    cancelling, setCancelling,
    isFocused, setIsFocused,
    blurPlaceholderKey, setBlurPlaceholderKey,
    agentSelectorOpen, setAgentSelectorOpen,
    modeSelectorOpen, setModeSelectorOpen,
    ringDragging, setRingDragging,
    isConnected,
    candidates, setCandidates,
    activeCandidateIndex, setActiveCandidateIndex,
    candidateItemRefs,
    bumpPromptCandidateRefresh,
    clearCandidates,
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
    clearActiveToken,
    editorRef,
    suppressedCommandCandidateTextRef,
    attachmentInputRef,
    uploadAbortRef,
    isComposingRef,
    compositionGuardUntilRef,
    selectedAgent,
    selectedModelInfo,
    availableEfforts,
    isCodexEffortAgent,
    supportsEffort,
    supportsServiceTier,
    supportsLongContext,
    effectiveModelForSend,
    planModeActive,
    planSessionKey,
    planRootId,
    sessionHistoryKey,
    isMultiLine,
    syncEditorHeight,
    resetEditorHeight,
    inputHistoryIndex,
    setInputHistoryIndex,
    applyingInputHistoryRef,
    inputHistoryDraftRef,
    navigateInputHistory,
    resetHistoryCursor,
    handleEditorChange,
    applyCandidate,
    removePromptLocally,
    handleSend,
    handleCancel,
    isCompositionActive,
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
    hideComposer,
    connectionMeta,
    accentColorRaw,
    accentHex,
    displayStatus,
  };
}
