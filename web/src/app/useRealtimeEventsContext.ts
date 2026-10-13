import type { Dispatch, RefObject, SetStateAction } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, Session, TokenUsage } from "../services/session";
import type { TaskDetail } from "../services/tasks";
import type { UpdateState } from "../services/update";
import type { ManagedRootPayload } from "./appMisc";
import type { MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, WSStatus } from "./appSession";
import type { SessionRuntimeMeta } from "./useSessionStreamCache";

/**
 * WS 事件处理器集合。2026-09 从 App.tsx 整块搬出：25 个 handler + 13 个内部 helper
 * （另有 handleSessionStream 内部的 9 个 case，合计 34 个事件分支）。
 *
 * 参数按用途分四组，组内一律「同名简写」传入——搬过来的 1600 行代码因此一个标识符都不用改。
 * 类型全部由 tsc 从 App 侧实参反推（不是手写、也不是 any），上游改名会立刻编不过。
 */
export type RealtimeEventsContext = {
  /** App 的 ref 池 */
  refs: {
    actionHandlersRef: RefObject<{ open: (params: any) => Promise<void>; open_dir: (params: any) => Promise<void>; }>;
    boundSessionByRootRef: RefObject<Record<string, string | null>>;
    cancelRequestedBySessionRef: RefObject<Record<string, boolean>>;
    currentRootIdRef: RefObject<string | null>;
    currentRootNodeIdRef: RefObject<string | null>;
    currentSessionRef: RefObject<SessionItem | null>;
    drawerSessionByRootRef: RefObject<Record<string, SessionItem | null>>;
    fileRef: RefObject<FilePayload | null>;
    invalidTreeCacheKeysRef: RefObject<Set<string>>;
    loadedSessionRef: RefObject<Record<string, boolean>>;
    managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
    managedRootByKeyRef: RefObject<Record<string, ManagedRootPayload>>;
    managedRootIdsRef: RefObject<Set<string>>;
    optimisticDequeuedIdsRef: RefObject<Record<string, Set<string>>>;
    pendingBySessionRef: RefObject<Record<string, PendingSend>>;
    pendingDraftRef: RefObject<PendingSend | null>;
    pendingRequestRef: RefObject<Record<string, PendingSend>>;
    queueFrozenBySessionRef: RefObject<Record<string, boolean>>;
    queuedMessagesBySessionRef: RefObject<Record<string, QueuedUserMessage[]>>;
    selectedDirRef: RefObject<string | null>;
    selectedSessionByRootRef: RefObject<Record<string, string | null>>;
    selectedSessionRef: RefObject<SessionItem | null>;
    sessionCacheRef: RefObject<Record<string, Session>>;
    sessionListReloadTimerRef: RefObject<number | null>;
    sessionsRef: RefObject<SessionItem[]>;
    suppressedAutoBindSessionByRootRef: RefObject<Record<string, string | null>>;
    taskDetailsByIdRef: RefObject<Record<string, TaskDetail>>;
    /** 主区是否停在跨项目工作台。工作台要收全部项目的 task.updated，不能只听当前项目。 */
    workspaceOpenRef: RefObject<boolean>;
  };
  /** App 的 setState */
  setters: {
    setAgentsVersion: Dispatch<SetStateAction<number>>;
    setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
    setCodexRateLimitsRefreshToken: Dispatch<SetStateAction<number>>;
    setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
    setGitDiff: Dispatch<SetStateAction<GitDiffPayload | null>>;
    setGitHubImportState: Dispatch<SetStateAction<GitHubImportState>>;
    setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
    setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => void;
    setProjectAddMode: Dispatch<SetStateAction<ProjectAddMode | null>>;
    setQueueVersion: Dispatch<SetStateAction<number>>;
    setRootEntries: Dispatch<SetStateAction<FileEntry[]>>;

    setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
    setSessions: Dispatch<SetStateAction<SessionItem[]>>;
    setSlashCommandResults: Dispatch<SetStateAction<Record<string, SlashCommandResult>>>;
    setStatus: Dispatch<SetStateAction<WSStatus>>;
    // 「标题 + 正文 + 详情清单」那个弹窗。收尾撞上冲突要走它：仓库停在 MERGE_HEAD，
    // 得把冲突文件逐条列出来，toast 里塞不下、也不会自己消失。
    setTaskSessionErrorDialog: Dispatch<SetStateAction<{ title: string; message: string; details: string[] } | null>>;
    setUpdateState: Dispatch<SetStateAction<UpdateState>>;
  };
  /** App 的回调与动作 */
  actions: {
    appendAgentChunkForSession: (rootID: string, sessionKey: string, content: string, runtimeHint?: SessionRuntimeMeta, eventTimestamp?: string, mindfsID?: string) => void;
    appendCompactNoticeForSession: (rootID: string, sessionKey: string, compactNotice: any, mindfsID?: string) => void;
    appendPlanUpdateForSession: (rootID: string, sessionKey: string, planUpdate: any, mindfsID?: string) => void;
    appendThoughtChunkForSession: (rootID: string, sessionKey: string, content: string, thoughtID?: string, mindfsID?: string) => void;
    appendTodoUpdateForSession: (rootID: string, sessionKey: string, todoUpdate: any, mindfsID?: string) => void;
    appendToolCallForSession: (rootID: string, sessionKey: string, toolCall: any, update: boolean, mindfsID?: string) => void;
    applyManagedRootRename: (oldRootID: string, rootPayload: ManagedRootPayload | null | undefined) => boolean;
    applyTaskDetails: (rootId: string, details: TaskDetail[], persist?: boolean) => void;
    attachContextWindowToLatestAssistant: (rootID: string, sessionKey: string, contextWindow?: { totalTokens?: number; modelContextWindow?: number; }, tokenUsage?: TokenUsage) => void;
    bumpCacheVersion: () => void;
    clearSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
    getNodeIdForRoot: (rootId: string) => string | undefined;
    invalidatePluginsForRoot: (rootId: string) => void;
    loadManagedRootPayloads: (opts?: { force?: boolean; }) => Promise<ManagedRootPayload[] | null>;
    loadMultiProjectSessionGroups: () => Promise<void>;
    /** 同上，但带 300ms 合并窗口——「新会话出现」这类会连发的事件用它 */
    scheduleMultiProjectSessionReload: () => void;
    loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => Promise<void>;
    markSessionPending: (rootID: string, sessionKey: string) => void;
    markSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
    playCompletionSound: () => void;
    /** 摘掉一个任务在本机的全部痕迹（task.deleted）。作用域由 rootId 划定。 */
    pruneTaskDetails: (rootId: string, keepTaskIds: Iterable<string>) => void;
    /** 重拉工作台结构。转发 ref，为 null 表示工作台没打开，调用方无需自己判。 */
    refreshWorkspaceBoard: () => void;
    promotePendingSessionForRoot: (rootID: string, tempKey: string | undefined, sessionKey: string, fallback?: Session | null) => void;
    refreshCurrentFileContent: (rootID: string, changedPath: string) => Promise<void>;
    refreshGitStatus: (rootID: string) => Promise<GitStatusPayload | null>;
    refreshManagedRoots: () => Promise<void>;
    refreshMultiProjectReplyingSessions: () => Promise<void>;
    refreshTaskWorktree: (rootId: string, worktreePath: string, force?: boolean) => Promise<void>;
    refreshTasksForRelatedSession: (root: string, sessionKey: string) => void;
    refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
    resolveRootForSessionKey: (sessionKey: string) => string | null;
    restoreActiveSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => Promise<Session | null>;
    scheduleSessionListReload: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => void;
    updateSessionAgentForKey: (rootID: string, sessionKey: string, agent: string, model?: string, agentMode?: string, effort?: string, fastService?: "" | "on" | "off", planMode?: boolean, shell?: string) => void;
    updateSessionRelatedFilesForKey: (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => void;
    updateSessionRelatedWorktreeForKey: (rootID: string, sessionKey: string, relatedWorktree: RelatedWorktree | null | undefined) => void;
  };
  /** 稳定值 */
  values: {
    currentRootId: string | null;
    gitDiff: GitDiffPayload | null;
    handleImportedSessionConfirmed: (agentSessionID: string, agentName: string) => void;
    multiProjectSessionsEnabled: boolean;
    rootSessionKey: (rootId: string, sessionKey: string) => string;
    scopedRootKey: (rootId: string) => string;
    t: I18nContextValue["t"];
    treeCacheKey: (rootID: string, dirPath: string) => string;
  };
};
