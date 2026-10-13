import { useCallback } from "react";
import type React from "react";
import type { I18nContextValue } from "../../i18n/index";
import type { AgentStatus } from "../../services/agents";
import type { CandidateItem } from "../../services/candidates";
import { reportError } from "../../services/net/error";
import { fileTokenPath, formatFileToken, isUploadAbortError, uploadFiles, type UploadProgress } from "../../services/upload";
import { matchesSendShortcut } from "../../services/prefs/sendShortcut";
import type { PendingAttachment, SessionMode, TokenEditorHandle } from "../../shared/actionBarTypes";
import { getAgentDefaults, has1MSuffix } from "./modelUtils";
import { replaceActiveTokenText } from "./inputTransforms";
import { parsePlanCommand, stripPlanCommandPrefix } from "../../shared/planCommand";
import type { ActionBarProps } from "./actionTypes";

type ActiveToken = { type: "file" | "slash" | "prompt" | "command"; query: string } | null;
type CandidateState = {
  candidates: CandidateItem[];
  setCandidates: React.Dispatch<React.SetStateAction<CandidateItem[]>>;
  activeCandidateIndex: number;
  setActiveCandidateIndex: React.Dispatch<React.SetStateAction<number>>;
  bumpPromptCandidateRefresh: () => void;
};

type UseActionBarHandlersParams = Pick<ActionBarProps,
  | "currentRootId"
  | "currentRootIsGitRepo"
  | "currentSession"
  | "mobileEnterKeySends"
  | "sendShortcut"
  | "onSendMessage"
  | "onSetPlanMode"
  | "onCancelCurrentTurn"
> & {
  mode: SessionMode;
  agent: string;
  setAgent: React.Dispatch<React.SetStateAction<string>>;
  setModel: React.Dispatch<React.SetStateAction<string>>;
  setAgentMode: React.Dispatch<React.SetStateAction<string>>;
  setEffort: React.Dispatch<React.SetStateAction<string>>;
  setFastService: React.Dispatch<React.SetStateAction<"" | "on" | "off">>;
  setLongContext: React.Dispatch<React.SetStateAction<boolean>>;
  agents: AgentStatus[];
  serializedInput: string;
  setSerializedInput: React.Dispatch<React.SetStateAction<string>>;
  activeToken: ActiveToken;
  setActiveToken: React.Dispatch<React.SetStateAction<ActiveToken>>;
  inputHistoryIndex: number | null;
  setInputHistoryIndex: React.Dispatch<React.SetStateAction<number | null>>;
  applyingInputHistoryRef: React.MutableRefObject<boolean>;
  inputHistoryDraftRef: React.MutableRefObject<string>;
  editorRef: React.RefObject<TokenEditorHandle | null>;
  suppressedCommandCandidateTextRef: React.MutableRefObject<string>;
  isComposingRef: React.MutableRefObject<boolean>;
  compositionGuardUntilRef: React.MutableRefObject<number>;
  candidates: CandidateState;
  pendingAttachments: PendingAttachment[];
  clearPendingAttachments: () => void;
  appendPendingAttachments: (files: File[]) => void;
  sending: boolean;
  setSending: React.Dispatch<React.SetStateAction<boolean>>;
  cancelling: boolean;
  setCancelling: React.Dispatch<React.SetStateAction<boolean>>;
  uploadAbortRef: React.MutableRefObject<AbortController | null>;
  setUploadProgress: React.Dispatch<React.SetStateAction<UploadProgress | null>>;
  isMobile: boolean;
  isConnected: boolean;
  effectiveModelForSend: string;
  agentMode: string;
  supportsEffort: boolean;
  effort: string;
  supportsServiceTier: boolean;
  fastService: "" | "on" | "off";
  shell: string;
  planSessionKey: string;
  planRootId: string;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  setCreateWorktree: React.Dispatch<React.SetStateAction<boolean>>;
  setWorktreeBranchMode: React.Dispatch<React.SetStateAction<"new" | "existing">>;
  setWorktreeBranch: React.Dispatch<React.SetStateAction<string>>;
  resetEditorHeight: () => void;
  syncEditorHeight: () => void;
  navigateInputHistory: (direction: "previous" | "next") => boolean;
  syncedSessionSignatureRef: React.MutableRefObject<string>;
  t: I18nContextValue["t"];
};

export function useActionBarHandlers(params: UseActionBarHandlersParams) {
  const { t } = params;
  const normalizedSendShortcut = params.sendShortcut ?? null;
  const {
    mode, agent, setAgent, setModel, setAgentMode, setEffort, setFastService, setLongContext, agents,
    currentRootId, currentRootIsGitRepo, currentSession, mobileEnterKeySends,
    onSendMessage, onSetPlanMode, onCancelCurrentTurn,
    serializedInput, setSerializedInput, activeToken, setActiveToken, inputHistoryIndex,
    setInputHistoryIndex, applyingInputHistoryRef, inputHistoryDraftRef, editorRef,
    suppressedCommandCandidateTextRef, isComposingRef, compositionGuardUntilRef,
    candidates: { candidates, setCandidates, activeCandidateIndex, setActiveCandidateIndex, bumpPromptCandidateRefresh },
    pendingAttachments, clearPendingAttachments, appendPendingAttachments,
    sending, setSending, cancelling, setCancelling, uploadAbortRef, setUploadProgress,
    isMobile, isConnected, effectiveModelForSend, agentMode, supportsEffort, effort,
    supportsServiceTier, fastService, shell, planSessionKey, planRootId,
    createWorktree, worktreeBranchMode, worktreeBranch, setCreateWorktree,
    setWorktreeBranchMode, setWorktreeBranch, resetEditorHeight, syncEditorHeight,
    navigateInputHistory, syncedSessionSignatureRef,
  } = params;

  const handleEditorChange = useCallback((payload: {
    serializedText: string;
    displayText: string;
    activeToken: ActiveToken;
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
  }, [applyingInputHistoryRef, inputHistoryDraftRef, inputHistoryIndex, mode, resetEditorHeight, setActiveToken, setInputHistoryIndex, setSerializedInput, suppressedCommandCandidateTextRef, syncEditorHeight]);

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
  }, [activeToken, editorRef, mode, serializedInput, setActiveCandidateIndex, setCandidates, setSerializedInput, suppressedCommandCandidateTextRef, syncEditorHeight]);

  const removePromptLocally = useCallback((text: string) => {
    setCandidates((items) => items.filter((item) => item.type !== "prompt" || item.name !== text));
    setActiveCandidateIndex(0);
    bumpPromptCandidateRefresh();
  }, [bumpPromptCandidateRefresh, setActiveCandidateIndex, setCandidates]);

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
          if (!isMobile) requestAnimationFrame(() => editorRef.current?.focus());
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
        attachmentTokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");
      }
      const payload = [messageText, attachmentTokens].filter(Boolean).join("\n");
      if (!payload) return;
      await onSendMessage?.(
        payload, mode, mode === "command" ? "" : agent, effectiveModelForSend || undefined,
        agentMode || undefined, supportsEffort ? effort || undefined : undefined,
        supportsServiceTier ? fastService : undefined, mode === "command" ? shell || undefined : undefined,
        !currentSession && mode !== "command" && currentRootIsGitRepo
          ? { create: createWorktree, branchMode: worktreeBranchMode, branch: worktreeBranchMode === "existing" ? worktreeBranch : "" }
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
      if (isMobile) requestAnimationFrame(() => editorRef.current?.blur());
    } catch (err) {
      if (!isUploadAbortError(err)) reportError("file.write_failed", String((err as Error)?.message || t("action.uploadFailed")));
    } finally {
      uploadAbortRef.current = null;
      setSending(false);
      setUploadProgress(null);
      if (!isMobile) requestAnimationFrame(() => editorRef.current?.focus());
    }
  }, [agent, agentMode, clearPendingAttachments, createWorktree, currentRootId, currentRootIsGitRepo, currentSession, editorRef, effectiveModelForSend, effort, fastService, inputHistoryDraftRef, isConnected, isMobile, mode, onSendMessage, onSetPlanMode, pendingAttachments, planRootId, planSessionKey, resetEditorHeight, setActiveCandidateIndex, setActiveToken, setCandidates, setCreateWorktree, setInputHistoryIndex, setSending, setSerializedInput, setUploadProgress, setWorktreeBranch, setWorktreeBranchMode, shell, supportsEffort, supportsServiceTier, t, uploadAbortRef, worktreeBranch, worktreeBranchMode]);

  const handleCancel = useCallback(async () => {
    const sessionKey = currentSession?.key;
    if (!sessionKey || cancelling) return;
    setCancelling(true);
    try {
      await onCancelCurrentTurn?.(sessionKey);
    } finally {
      setCancelling(false);
    }
  }, [cancelling, currentSession?.key, onCancelCurrentTurn, setCancelling]);

  const isCompositionActive = useCallback((event?: KeyboardEvent | null) => {
    const nativeEvent = event as (KeyboardEvent & { isComposing?: boolean; keyCode?: number }) | null | undefined;
    return isComposingRef.current || performance.now() < compositionGuardUntilRef.current || !!nativeEvent?.isComposing || nativeEvent?.keyCode === 229;
  }, [compositionGuardUntilRef, isComposingRef]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (isCompositionActive(e.nativeEvent)) return;
    if (e.key !== "Enter" && !e.repeat && matchesSendShortcut(e.nativeEvent, normalizedSendShortcut)) {
      e.preventDefault(); e.stopPropagation(); void handleSend(); return;
    }
    if (candidates.length > 0) {
      if (e.key === "ArrowDown") { e.preventDefault(); setActiveCandidateIndex((prev) => (prev < 0 ? 0 : (prev + 1) % candidates.length)); return; }
      if (e.key === "ArrowUp") { e.preventDefault(); setActiveCandidateIndex((prev) => (prev < 0 ? candidates.length - 1 : (prev - 1 + candidates.length) % candidates.length)); return; }
      if (e.key === "Tab") { e.preventDefault(); if (activeCandidateIndex >= 0) applyCandidate(candidates[activeCandidateIndex]); return; }
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); setCandidates([]); setActiveCandidateIndex(0); return; }
    }
    if (!activeToken && !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
      if (e.key === "ArrowUp" && navigateInputHistory("previous")) { e.preventDefault(); e.stopPropagation(); return; }
      if (e.key === "ArrowDown" && navigateInputHistory("next")) { e.preventDefault(); e.stopPropagation(); }
    }
  }, [activeCandidateIndex, activeToken, applyCandidate, candidates, handleSend, isCompositionActive, navigateInputHistory, normalizedSendShortcut, setActiveCandidateIndex, setCandidates]);

  const handleEditorEnter = useCallback((event: KeyboardEvent | null) => {
    if (isCompositionActive(event)) return true;
    if (event && !event.repeat && matchesSendShortcut(event, normalizedSendShortcut)) { event.preventDefault(); event.stopPropagation(); void handleSend(); return true; }
    if (event?.shiftKey) return false;
    if (candidates.length > 0) {
      if (activeCandidateIndex >= 0) { event?.preventDefault(); event?.stopPropagation(); applyCandidate(candidates[activeCandidateIndex]); return true; }
      if (mode !== "command") { event?.preventDefault(); event?.stopPropagation(); applyCandidate(candidates[0]); return true; }
    }
    if (!isMobile || (mobileEnterKeySends && mode === "chat")) { event?.preventDefault(); event?.stopPropagation(); void handleSend(); return true; }
    return false;
  }, [activeCandidateIndex, applyCandidate, candidates, handleSend, isCompositionActive, isMobile, mobileEnterKeySends, mode, normalizedSendShortcut]);

  const handleEditorPaste = useCallback((event: React.ClipboardEvent<HTMLDivElement>) => {
    if (sending || !currentRootId) return;
    const clipboardItems = Array.from(event.clipboardData?.items || []);
    const imageFiles = clipboardItems.filter((item) => item.kind === "file" && item.type.startsWith("image/"))
      .map((item) => item.getAsFile()).filter((file): file is File => !!file);
    if (imageFiles.length === 0) return;
    event.preventDefault();
    appendPendingAttachments(imageFiles);
  }, [appendPendingAttachments, currentRootId, sending]);

  const resetForNewSession = useCallback(() => {
    const nextAgent = agents.find((item) => item.name === agent) || agents.find((item) => item.available) || agents[0];
    if (!nextAgent) return;
    const defaults = getAgentDefaults(nextAgent);
    setAgent(nextAgent.name); setModel(defaults.model); setAgentMode(""); setEffort(defaults.effort);
    setFastService(defaults.fastService); setLongContext(has1MSuffix(defaults.model));
    syncedSessionSignatureRef.current = "";
  }, [agent, agents, setAgent, setAgentMode, setEffort, setFastService, setLongContext, setModel, syncedSessionSignatureRef]);

  return { handleEditorChange, applyCandidate, removePromptLocally, handleSend, handleCancel, isCompositionActive, handleKeyDown, handleEditorEnter, handleEditorPaste, resetForNewSession };
}
