import { type SessionMode } from "../ModeSelector";
import { type MessageKey } from "../../i18n";
import { type SendShortcut } from "../../services/sendShortcut";
import { type UploadProgress } from "../../services/upload";

export type SessionInfo = {
  key: string;
  session_key?: string;
  root_id?: string;
  name: string;
  type: "chat" | "plugin" | "command";
  agent: string;
  model?: string;
  shell?: string;
  mode?: string;
  effort?: string;
  fast_service?: string;
  plan_mode?: boolean;
  pending?: boolean;
};

export type AttachedFileContext = {
  filePath: string;
  fileName: string;
  startLine?: number;
  endLine?: number;
  text?: string;
};

export type QueuedMessageInfo = {
  id: string;
  content: string;
  created_at?: string;
};

export type WSStatus = "connecting" | "connected" | "reconnecting" | "disconnected";

export type { SessionMode, MessageKey, SendShortcut, UploadProgress };

export type ActionBarProps = {
  status?: WSStatus;
  agentsVersion?: number;
  codexRateLimitsRefreshToken?: number;
  currentRootId?: string | null;
  currentRootIsGitRepo?: boolean;
  currentSession?: SessionInfo | null;
  pendingPlanMode?: boolean;
  rootColor?: string | null;
  attachedFileContext?: AttachedFileContext | null;
  canOpenSessionDrawer?: boolean;
  /** 看板/工作台不是对话语境：不渲染输入区，移动端只留呼出左右侧栏的按钮。 */
  hideComposer?: boolean;
  sessionDrawerOpen?: boolean;
  detachedBoundSession?: boolean;
  editDraftRequest?: {
    id: number;
    content: string;
  } | null;
  queuedMessages?: QueuedMessageInfo[];
  inputHistory?: string[];
  mobileEnterKeySends?: boolean;
  sendShortcut?: SendShortcut | null;
  onSendMessage?: (
    message: string,
    mode: SessionMode,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: "" | "on" | "off",
    shell?: string,
    newSessionWorktree?: {
      create: boolean;
      branchMode: "new" | "existing";
      branch: string;
    },
  ) => void | Promise<void>;
  onSetPlanMode?: (
    enabled: boolean,
    sessionKey?: string,
    rootId?: string,
  ) => void | Promise<void>;
  onCancelCurrentTurn?: (sessionKey: string) => void;
  onRemoveQueuedMessage?: (queueId: string) => void | Promise<void>;
  onUpdateQueuedMessage?: (queueId: string, content: string) => void | Promise<void>;
  onSendQueuedMessageNow?: (queueId: string) => void | Promise<void>;
  onNewSession?: () => void;
  onRequestFileContext?: () => void;
  onClearFileContext?: () => void;
  onSessionClick?: () => void;
  onToggleLeftSidebar?: () => void;
  onToggleRightSidebar?: () => void;
  sidebarsSwapped?: boolean;
};
