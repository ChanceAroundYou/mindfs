// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { hasSessionExchanges, isSessionShownInMain, isTopLevelSessionItem, mergeReplyingStateByNode, normalizeMode, relatedFileSelectionKey, sessionInputHistory, shouldAutoSelectNewSession, toSessionItem } from "../../app/appSession";
import { ActionBar } from "../action/ActionBar";
import type { Dispatch, SetStateAction, RefObject } from "react";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { SESSION_WINDOW_SIZE, clearCachedSessionsForRoot, clearWindowedView, composeLoadedExchanges, deleteCachedSession, deleteCachedSessionLists, getCachedSession, getCachedSessionList, getSessionWindow, isTransientExchange, saveCachedSessionList, sessionService, setCachedSessionRelatedFiles, setWindowedView, settlePendingAcks, syncSession, type QueuedUserMessage, type RelatedFile, type RelatedWorktree, type Session, type TokenUsage } from "../../services/session";
import {
  loadSendShortcut,
  persistSendShortcut,
  type SendShortcut,
} from "../../services/prefs/sendShortcut";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "../../app/appMisc";
import { MainViewMode, URLState } from "../../app/appPath";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "../../app/appSession";
import { useActionHandlers, type ActionHandlers } from "../../app/useActionHandlers";

export interface AppActionBarProps {
  actionBarInputHistory: string[];
  actionBarQueuedMessages: QueuedUserMessage[];
  actionBarSession: any;
  actionHandlers: ActionHandlers;
  agentsVersion: number;
  attachedFileContext: AttachedFileContext | null;
  canOpenSessionDrawer: boolean;
  codexRateLimitsRefreshToken: number;
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  currentRootSlashCommandResult: SlashCommandResult | null;
  detachedBoundSession: boolean;
  drawerOpenByRootRef: RefObject<Record<string, boolean>>;
  editDraftRequest: { id: number; content: string; } | null;
  getDisplayNodeColor: (rootId: string) => string | null;
  handleCancelCurrentTurn: (sessionKey: string) => Promise<void>;
  handleClearFileContext: () => void;
  handleNewSession: () => void;
  handleRemoveQueuedMessage: (queueId: string) => Promise<void>;
  handleRequestFileContext: () => void;
  handleSelectSession: (session: any, options?: { preserveTaskSelection?: boolean | undefined; preserveMainView?: boolean | undefined; } | undefined) => Promise<void>;
  handleSendMessage: (message: string, mode: SessionMode, agent: string, model?: string | undefined, agentMode?: string | undefined, effort?: string | undefined, fastService?: "" | "on" | "off" | undefined, shell?: string | undefined, newSessionWorktree?: { create: boolean; branchMode: "new" | "existing"; branch: string; } | undefined) => Promise<void>;
  handleSendQueuedMessageNow: (queueId: string) => Promise<void>;
  handleSetPlanMode: (enabled: boolean, targetSessionKey?: string | undefined, targetRootId?: string | undefined) => Promise<void>;
  handleUpdateQueuedMessage: (queueId: string, content: string) => Promise<void>;
  interactionModeRef: RefObject<"main" | "drawer">;
  isDrawerOpen: boolean;
  isMobile: boolean;
  mainView: MainViewMode;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  mobileEnterKeySends: boolean;
  normalizeMode: any;
  pendingPlanMode: boolean;
  renderRootSlashCommandResult: (result: SlashCommandResult | null) => React.JSX.Element | null;
  scopedRootKey: (rootId: string) => string;
  sendShortcut: SendShortcut | null;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  setInteractionMode: Dispatch<SetStateAction<"main" | "drawer">>;
  status: WSStatus;
}

export function AppActionBar(props: AppActionBarProps) {
  const actionBarInputHistory = props.actionBarInputHistory;
  const actionBarQueuedMessages = props.actionBarQueuedMessages;
  const actionBarSession = props.actionBarSession;
  const actionHandlers = props.actionHandlers;
  const agentsVersion = props.agentsVersion;
  const attachedFileContext = props.attachedFileContext;
  const canOpenSessionDrawer = props.canOpenSessionDrawer;
  const codexRateLimitsRefreshToken = props.codexRateLimitsRefreshToken;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const currentRootSlashCommandResult = props.currentRootSlashCommandResult;
  const detachedBoundSession = props.detachedBoundSession;
  const drawerOpenByRootRef = props.drawerOpenByRootRef;
  const editDraftRequest = props.editDraftRequest;
  const getDisplayNodeColor = props.getDisplayNodeColor;
  const handleCancelCurrentTurn = props.handleCancelCurrentTurn;
  const handleClearFileContext = props.handleClearFileContext;
  const handleNewSession = props.handleNewSession;
  const handleRemoveQueuedMessage = props.handleRemoveQueuedMessage;
  const handleRequestFileContext = props.handleRequestFileContext;
  const handleSelectSession = props.handleSelectSession;
  const handleSendMessage = props.handleSendMessage;
  const handleSendQueuedMessageNow = props.handleSendQueuedMessageNow;
  const handleSetPlanMode = props.handleSetPlanMode;
  const handleUpdateQueuedMessage = props.handleUpdateQueuedMessage;
  const interactionModeRef = props.interactionModeRef;
  const isDrawerOpen = props.isDrawerOpen;
  const isMobile = props.isMobile;
  const mainView = props.mainView;
  const managedRootByIdRef = props.managedRootByIdRef;
  const mobileEnterKeySends = props.mobileEnterKeySends;
  const normalizeMode = props.normalizeMode;
  const pendingPlanMode = props.pendingPlanMode;
  const renderRootSlashCommandResult = props.renderRootSlashCommandResult;
  const scopedRootKey = props.scopedRootKey;
  const sendShortcut = props.sendShortcut;
  const setDrawerOpenForRoot = props.setDrawerOpenForRoot;
  const setInteractionMode = props.setInteractionMode;
  const status = props.status;

  return (
    <>
    {
              <div
                style={{
                  width: "100%",
                  minWidth: 0,
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "stretch",
                  background: "var(--content-bg)",
                }}
              >
                {currentRootSlashCommandResult ? (
                  <div
                    style={{
                      width: "100%",
                      boxSizing: "border-box",
                      padding: isMobile ? "0 0 6px" : "0 16px 8px",
                    }}
                  >
                    {renderRootSlashCommandResult(currentRootSlashCommandResult)}
                  </div>
                ) : null}
                <ActionBar
                  onNewSession={handleNewSession}
                  onSelectProject={(root) => {
                    void actionHandlers.open_dir({ path: root, root, isRoot: true, suppressTreeExpand: true });
                  }}
                  onSelectSession={(session) => { void handleSelectSession(session); }}
                  status={status}
                  agentsVersion={agentsVersion}
                  codexRateLimitsRefreshToken={codexRateLimitsRefreshToken}
                  currentRootId={currentRootId}
                  currentRootIsGitRepo={managedRootByIdRef.current[currentRootId || ""]?.is_git_repo === true}
                  currentSession={actionBarSession ? { ...actionBarSession, name: actionBarSession.name || "", type: normalizeMode(actionBarSession.type), agent: actionBarSession.agent || "" } : null}
                  pendingPlanMode={pendingPlanMode}
                  rootColor={getDisplayNodeColor(String(currentRootId || ""))}
                  attachedFileContext={attachedFileContext}
                  canOpenSessionDrawer={canOpenSessionDrawer}
                  hideComposer={mainView === "board" || mainView === "workspace"}
                  sessionDrawerOpen={isDrawerOpen}
                  detachedBoundSession={detachedBoundSession}
                  editDraftRequest={editDraftRequest}
                  queuedMessages={actionBarQueuedMessages}
                  inputHistory={actionBarInputHistory}
                  onSendMessage={handleSendMessage}
                  onSetPlanMode={handleSetPlanMode}
                  onCancelCurrentTurn={handleCancelCurrentTurn}
                  onRemoveQueuedMessage={handleRemoveQueuedMessage}
                  onUpdateQueuedMessage={handleUpdateQueuedMessage}
                  onSendQueuedMessageNow={handleSendQueuedMessageNow}
                  mobileEnterKeySends={mobileEnterKeySends}
                  sendShortcut={sendShortcut}
                  onRequestFileContext={handleRequestFileContext}
                  onClearFileContext={handleClearFileContext}
                  onSessionClick={() => {
                  const rootID = currentRootIdRef.current;
                  // 直接用 canOpenSessionDrawer：它已经把「会话是否真的在主区」算全了。
                  // 这里若单独重算一份（漏掉 mainView 前提），文件态会被误判成「已在主区」而点了没反应。
                  if (!canOpenSessionDrawer) return;
                  const isDrawerCurrentlyOpen =
                    !!drawerOpenByRootRef.current[scopedRootKey(rootID || "")];
                  if (isDrawerCurrentlyOpen) {
                    interactionModeRef.current = "main";
                    setInteractionMode("main");
                    setDrawerOpenForRoot(rootID, false);
                    return;
                  }
                  setInteractionMode("drawer");
                  setDrawerOpenForRoot(rootID, true);
                  }}
                />
              </div>
            }
    </>
  );
}
