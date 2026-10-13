import type {
  AttachedFileContext,
  MultiProjectSessionGroup,
  PendingSend,
  SessionItem,
  SlashCommandResult,
} from "./appSession";
import type { ManagedRootPayload } from "./appMisc";
import type { MainViewMode } from "./appPath";
import type { FilePayload } from "../services/file";
import type { PluginManager } from "../plugins/manager";
import type { Session } from "../services/session";

/** handleSendMessage 的完整参数面：35 个闭包。 */
export type SendMessageParams = {
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  currentRootIdRef: React.RefObject<string | null>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  fileRef: React.RefObject<FilePayload | null>;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  mainViewRef: React.RefObject<MainViewMode>;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  pendingDraftRef: React.RefObject<PendingSend | null>;
  pendingRequestRef: React.RefObject<Record<string, PendingSend>>;
  pluginBypassRef: React.RefObject<boolean>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginQueryRef: React.RefObject<Record<string, string>>;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setSlashCommandResults: React.Dispatch<React.SetStateAction<Record<string, SlashCommandResult>>>;
  setMultiProjectSessionGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  setPendingPlanMode: React.Dispatch<React.SetStateAction<boolean>>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  attachedFileContext: AttachedFileContext | null;
  pendingPlanMode: boolean;
  slashCommandResults: Record<string, SlashCommandResult>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => void;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  bumpCacheVersion: () => void;
  clearLocalPendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
  resolvePendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined, fallback?: boolean) => boolean;
  clearSlashCommandResultForSession: (rootID: string, sessionKey: string) => void;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  scopedRootKey: (rootId: string) => string;
  updateSessionAgentForKey: (
    rootID: string,
    sessionKey: string,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: "" | "on" | "off",
    planMode?: boolean,
    shell?: string,
  ) => void;
};
