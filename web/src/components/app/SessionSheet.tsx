// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { BottomSheet } from "../shell/BottomSheet";
import { SessionViewer } from "../session/SessionViewer";
import { useI18n } from "../../i18n";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { fetchAgents, restartAgent, type AgentStatus } from "../../services/agents";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "../../app/appMisc";
import { MainViewMode, URLState } from "../../app/appPath";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "../../app/appSession";
import { useActionHandlers, type ActionHandlers } from "../../app/useActionHandlers";

export interface SessionSheetProps {
  BottomSheet: any;
  actionHandlers: ActionHandlers;
  availableAgents: AgentStatus[];
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  currentSession: SessionItem | null;
  drawerLoadingSessionByRoot: Record<string, string>;
  drawerScrollRef: RefObject<HTMLDivElement | null>;
  drawerSessionSnapshot: any;
  getDisplayNodeColor: (rootId: string) => string | null;
  gitFileStatsByPath: Record<string, GitFileStat>;
  handleAskUserAnswer: (input: { rootId: string; sessionKey: string; agent?: string | undefined; toolUseId: string; answers: Record<string, string>; }) => Promise<void>;
  handleDrawerSessionFileClick: (target: string | RelatedFileClickTarget) => void;
  handleEditUserMessage: (content: string) => void;
  handleForkAgentMessage: (rootID: string | null | undefined, sessionKey: string | null | undefined, seq: number) => Promise<void>;
  handleRemoveSessionRelatedFile: (rootID: string | null | undefined, sessionKey: string | undefined, path: string, head?: string, repoPath?: string, repoKind?: string) => Promise<void>;
  handleSelectSession: (session: any, options?: { preserveTaskSelection?: boolean | undefined; preserveMainView?: boolean | undefined; } | undefined) => Promise<void>;
  interactionModeRef: RefObject<"main" | "drawer">;
  isDrawerOpen: boolean;
  mainView: MainViewMode;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  selectedKanbanTaskId: string;
  sessionViewerComposerOverlayInset: 20;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  setInteractionMode: Dispatch<SetStateAction<"main" | "drawer">>;
  slashCommandResultForSession: (rootID: string | null | undefined, session: { key?: string | undefined; session_key?: string | undefined; } | null | undefined) => SlashCommandResult | null;
  t: ReturnType<typeof useI18n>["t"];
}

export function SessionSheet(props: SessionSheetProps) {
  const BottomSheet = props.BottomSheet;
  const actionHandlers = props.actionHandlers;
  const availableAgents = props.availableAgents;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const currentSession = props.currentSession;
  const drawerLoadingSessionByRoot = props.drawerLoadingSessionByRoot;
  const drawerScrollRef = props.drawerScrollRef;
  const drawerSessionSnapshot = props.drawerSessionSnapshot;
  const getDisplayNodeColor = props.getDisplayNodeColor;
  const gitFileStatsByPath = props.gitFileStatsByPath;
  const handleAskUserAnswer = props.handleAskUserAnswer;
  const handleDrawerSessionFileClick = props.handleDrawerSessionFileClick;
  const handleEditUserMessage = props.handleEditUserMessage;
  const handleForkAgentMessage = props.handleForkAgentMessage;
  const handleRemoveSessionRelatedFile = props.handleRemoveSessionRelatedFile;
  const handleSelectSession = props.handleSelectSession;
  const interactionModeRef = props.interactionModeRef;
  const isDrawerOpen = props.isDrawerOpen;
  const mainView = props.mainView;
  const managedRootByIdRef = props.managedRootByIdRef;
  const selectedKanbanTaskId = props.selectedKanbanTaskId;
  const sessionViewerComposerOverlayInset = props.sessionViewerComposerOverlayInset;
  const setDrawerOpenForRoot = props.setDrawerOpenForRoot;
  const setInteractionMode = props.setInteractionMode;
  const slashCommandResultForSession = props.slashCommandResultForSession;
  const t = props.t;

  return (
    <>
    {
              <BottomSheet
                // 悬浮框只属于文件态：离开文件态一律不显示。
                isOpen={isDrawerOpen && mainView === "files"}
                contentRef={drawerScrollRef}
                onClose={() => {
                  interactionModeRef.current = "main";
                  setInteractionMode("main");
                  setDrawerOpenForRoot(currentRootIdRef.current, false);
                }}
                onExpand={() => {
                  handleSelectSession(currentSession, {
                    preserveTaskSelection:
                      !!currentSession?.task_id &&
                      currentSession.task_id === selectedKanbanTaskId,
                  });
                  setDrawerOpenForRoot(currentRootIdRef.current, false);
                }}
              >
                {drawerSessionSnapshot ? (
                  <SessionViewer
                    // 同主区：切会话必须重挂载，否则窗口化可见集会跨会话复用（闪动成因）。
                    key={drawerSessionSnapshot.key || "no-drawer-session"}
                    session={drawerSessionSnapshot}
                    agents={availableAgents}
                    slashCommandResult={slashCommandResultForSession(
                      currentRootId,
                      drawerSessionSnapshot,
                    )}
                    targetSeq={currentSession?.search_seq}
                    targetSeqRequestKey={currentSession?.search_target_id}
                    composerOverlayInset={sessionViewerComposerOverlayInset}
                    loading={
                      drawerLoadingSessionByRoot[currentRootId || ""] ===
                      (drawerSessionSnapshot.key || drawerSessionSnapshot.session_key)
                    }
                    rootId={currentRootId}
                    rootColor={getDisplayNodeColor(String(currentRootId || ""))}
                    rootPath={
                      managedRootByIdRef.current[currentRootId || ""]?.root_path ||
                      null
                    }
                    interactionMode="drawer"
                    scrollContainerRef={drawerScrollRef}
                    gitFileStatsByPath={gitFileStatsByPath}
                    onFileClick={handleDrawerSessionFileClick}
                    onRootClick={(root) => {
                      void actionHandlers.open_dir({
                        path: root,
                        root,
                        isRoot: true,
                        forceDirectory: true,
                        suppressTreeExpand: true,
                      });
                    }}
                    onRemoveRelatedFile={(path, head, repoPath, repoKind) =>
                      void handleRemoveSessionRelatedFile(
                        currentRootId,
                        drawerSessionSnapshot?.key || drawerSessionSnapshot?.session_key,
                        path,
                        head,
                        repoPath,
                        repoKind,
                      )
                    }
                    onAskUserAnswer={handleAskUserAnswer}
                    onEditUserMessage={handleEditUserMessage}
                    onForkAgentMessage={(seq) =>
                      void handleForkAgentMessage(
                        currentRootId,
                        drawerSessionSnapshot?.key || drawerSessionSnapshot?.session_key,
                        seq,
                      )
                    }
                  />
                ) : (
                  <div style={{ padding: "40px", textAlign: "center" }}>
                    {t("task.startHint")}
                  </div>
                )}
              </BottomSheet>
            }
    </>
  );
}
