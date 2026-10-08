import type React from "react";
import type { AgentStatus } from "../../services/agents";
import type { ExchangeAux, RelatedFile, TokenUsage } from "../../services/session";

export type SessionItem = {
  key?: string;
  session_key?: string;
  type?: string;
  name?: string;
  agent?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  search_seq?: number;
  scope?: string;
  purpose?: string;
  exchanges?: Array<{
    seq?: number;
    role?: string;
    agent?: string;
    model?: string;
    model_display_name?: string;
    effort?: string;
    fast_service?: string;
    content?: string;
    timestamp?: string;
    context_window?: {
      totalTokens: number;
      modelContextWindow: number;
    };
    token_usage?: TokenUsage;
  }>;
  closed_at?: string;
  source?: string;
  related_files?: RelatedFile[];
  exchange_aux?: Record<string, ExchangeAux[]>;
};

export type ExchangeArray = NonNullable<SessionItem["exchanges"]>;

export type SessionViewerProps = {
  session: SessionItem | null;
  loading?: boolean;
  slashCommandResult?: {
    sessionKey?: string;
    command: string;
    content: string;
    status: "running" | "complete" | "failed";
    error?: string;
    loginNotice?: {
      status?: string;
      loginId?: string;
      verificationUrl?: string;
      userCode?: string;
      error?: string;
      authMode?: string;
      planType?: string;
    };
  } | null;
  rootId?: string | null;
  rootDisplayName?: string | null;
  rootPath?: string | null;
  rootColor?: string | null;
  interactionMode?: "main" | "drawer";
  targetSeq?: number;
  gitFileStatsByPath?: Record<
    string,
    { status: string; additions: number; deletions: number }
  >;
  onFileClick?: (file: RelatedFile & { name?: string }) => void;
  onRootClick?: (rootId: string) => void;
  onRemoveRelatedFile?: (path: string, head?: string, repoPath?: string, repoKind?: string) => void;
  onAskUserAnswer?: (input: {
    rootId: string;
    sessionKey: string;
    agent?: string;
    toolUseId: string;
    answers: Record<string, string>;
  }) => void | Promise<void>;
  onEditUserMessage?: (content: string) => void;
  onForkAgentMessage?: (seq: number) => void | Promise<void>;
  targetSeqRequestKey?: string | number;
  agents?: AgentStatus[];
  composerOverlayInset?: number;
  scrollContainerRef?: React.RefObject<HTMLDivElement | null>;
};

export type AskUserQuestionOption = {
  label?: string;
  description?: string;
};

export type AskUserQuestionItem = {
  question?: string;
  header?: string;
  options?: AskUserQuestionOption[];
  multiSelect?: boolean;
};

export type UploadAttachment = {
  path: string;
  name: string;
  isImage: boolean;
};
