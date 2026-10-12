// 会话服务的数据类型（从 services/session.ts 抽出，2026-10-07）。
//
// 全是**纯类型**：运行期被擦除，所以这次搬移对 VM 测试、source-map 守卫、e2e
// 零影响。留在 session.ts 的只有 `SESSION_WINDOW_SIZE`（G-AT 锚点要求那句话
// 逐字在 session.ts 里）与 `PENDING_STREAM_LIMIT`（属于 WS 传输的运行时行为）。
//
// 三个原本私有的类型（SessionEventHandler / SessionServiceEvent / PendingMessage）
// 在这里必须导出才能跨模块引用，但 session.ts **不**再导出它们 —— 公开面不变。

export type SessionType = "chat" | "plugin" | "command";

export type QueuedUserMessage = {
  id: string;
  agent?: string;
  model?: string;
  mode?: string;
  effort?: string;
  fast_service?: string;
  content: string;
  timestamp: string;
};

export type TokenUsage = {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
};

export type RelatedFile = {
  root_id?: string;
  repo_path?: string;
  repo_name?: string;
  repo_kind?: "git" | "plain" | string;
  path: string;
  head?: string;
  relation?: string;
  created_by_session?: boolean;
};

export type RelatedWorktree = {
  root_id: string;
  path: string;
  branch?: string;
  head?: string;
  current?: boolean;
  updated_at?: string;
};

export type ExchangeAux = {
  seq: number;
  line: number;
  toolcall?: ToolCall | null;
  thought?: string | null;
  thought_id?: string;
  todo?: TodoUpdate | null;
  plan?: PlanUpdate | null;
  compact?: CompactNotice | null;
};

export type SessionBlock = {
  seq: number;
  pos: number;
  revision?: number;
  type: "reasoning" | "text" | "tool_call" | "todo" | "plan" | "compact" | string;
  text?: string;
  toolcall?: ToolCall | null;
  todo?: TodoUpdate | null;
  plan?: PlanUpdate | null;
  compact?: CompactNotice | null;
  step?: number;
  /** MindFS 权威身份：写入点统一铸、全系统唯一不变（G-BI 从 turn_id 改名而来） */
  mindfs_id?: string;
  /** agent 侧桥梁键（claude 转录 uuid / ACP messageId），可空，不参与系统内唯一性 */
  agent_id?: string;
  parent_tool_use_id?: string;
  task_id?: string;
  subagent_type?: string;
};

export type Session = {
  key: string;
  session_key?: string;
  root_id?: string;
  type: SessionType;
  parent_session_key?: string;
  parent_tool_call_id?: string;
  source?: string;
  task_id?: string;
  agent?: string;
  model?: string;
  shell?: string;
  mode?: string;
  effort?: string;
  fast_service?: string;
  plan_mode?: boolean;
  name: string;
  created_at: string;
  updated_at: string;
  closed_at?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  related_files?: RelatedFile[];
  related_worktree?: RelatedWorktree | null;
  pinned_at?: string | null;
  /** 持久化缓存截断标记：true 表示缓存只含最近 N 条 exchanges，读取方应全量拉取补段 */
  truncated?: boolean;
  exchange_aux?: Record<string, ExchangeAux[]>;
  /**
   * 窗口内 seq 的正文块，形状与 exchange_aux 一致。
   * 缺失 / 为空 = 该会话还没有块流（本方案落地前的历史会话）→ 渲染回退旧格式。
   */
  blocks?: Record<string, SessionBlock[]>;
  exchanges?: Array<{
    seq?: number;
    role?: string;
    agent?: string;
    model?: string;
    model_display_name?: string;
    mode?: string;
    effort?: string;
    fast_service?: string;
    content?: string;
    context_window?: {
      totalTokens: number;
      modelContextWindow: number;
    };
    token_usage?: TokenUsage;
    timestamp?: string;
    toolCall?: ToolCall;
    todoUpdate?: TodoUpdate;
    planUpdate?: PlanUpdate;
    compactNotice?: CompactNotice;
    pending_ack?: boolean;
  }>;
};

export type SessionSearchHit = {
  root_id?: string;
  key: string;
  type: SessionType;
  parent_session_key?: string;
  parent_tool_call_id?: string;
  source?: string;
  agent?: string;
  model?: string;
  shell?: string;
  name: string;
  created_at: string;
  updated_at: string;
  closed_at?: string;
  match_type: "name" | "user" | "reply";
  match_score: number;
  seq: number;
  snippet?: string;
};

export type ToolCallLocation = {
  path: string;
  line?: number;
};

export type ToolCallContentItem =
  | {
      type: "text";
      text?: string;
      path?: string;
      changeKind?: string;
    }
  | {
      type: "diff";
      path?: string;
      oldText?: string;
      newText?: string;
      changeKind?: string;
    };

export type ToolCall = {
  callId: string;
  title?: string;
  status: string;
  kind: string;
  content?: ToolCallContentItem[];
  locations?: ToolCallLocation[];
  meta?: Record<string, unknown>;
  rawType?: string;
};

export type TodoItem = {
  content: string;
  activeForm?: string;
  status: string;
};

export type TodoUpdate = {
  items: TodoItem[];
};

export type PlanUpdate = {
  id?: string;
  content: string;
  delta?: boolean;
};

export type CompactNotice = {
  id?: string;
  status?: string;
  summary?: string;
};

/**
 * 实时流事件。`mindfs_id` 由后端 G-BI 2cf2858 补上：user_message 与 session.stream
 * 帧携带本轮身份，前端实时缓存据此对账（见 appSession.ts 的 Exchange.mindfs_id）。
 */
export type StreamEvent = {
  event_cursor?: string;
  timestamp?: string;
  mindfs_id?: string;
} & (
  | { type: "message_chunk"; data: { content: string } }
  | { type: "thought_chunk"; data: { id?: string; content: string } }
  | { type: "tool_call"; data: ToolCall }
  | { type: "tool_call_update"; data: ToolCall }
  | { type: "todo_update"; data: TodoUpdate }
  | { type: "plan_update"; data: PlanUpdate }
  | { type: "compact_notice"; data: CompactNotice }
  | { type: "recovery"; data: { message: string } }
  | {
      type: "message_done";
      data?: {
        contextWindow?: {
          totalTokens: number;
          modelContextWindow: number;
        };
        tokenUsage?: TokenUsage;
      };
    }
  | { type: "error"; data: { message: string } }
);

export type SyncSessionResult = {
  session: Session | null;
  hasDelta: boolean;
};

/** 服务端窗口化加载的元数据（方案 B：超长会话按需加载）。 */
export type SessionWindowMeta = {
  total: number;
  hasMore: boolean;
  minSeq: number;
  maxSeq: number;
};

/** 单窗口的会话数据 + 窗口元数据，供 SessionViewer 渐进式渲染。 */
export type SessionWindow = {
  session: Session | any;
  meta: SessionWindowMeta;
  raw?: any;
};

/** getSessionWindow 的可选参数。before_seq 与 latest 互斥，由后端校验。 */
export type SessionWindowOptions = {
  beforeSeq?: number;
  limit?: number;
  latest?: number;
  nodeId?: string;
};

export type SessionEventHandler = {
  onStream?: (event: StreamEvent) => void;
  onDone?: () => void;
  onError?: (error: string) => void;
};

export type SessionServiceEvent = {
  type: string;
  sessionKey?: string;
  payload?: Record<string, unknown>;
};

export type FetchSessionsOptions = {
  beforeTime?: string;
  afterTime?: string;
  limit?: number;
  topLevel?: boolean;
  includeChildren?: boolean;
  archivedOnly?: boolean;
};

export type SessionListPayload = {
  items: Session[];
  pinnedItems: Session[];
  pinnedKeys: string[];
  totalCount: number;
};

export type MultiRootSessionGroup = {
  rootId: string;
  rootName: string;
  latestSessionTime: string;
  items: Session[];
  pinnedItems: Session[];
  pinnedKeys: string[];
  totalCount: number;
};

export type FetchExternalSessionsOptions = {
  beforeTime?: string;
  afterTime?: string;
  filterBound?: boolean;
  limit?: number;
};

export type PendingMessage = {
  id: string;
  message: Record<string, unknown>;
};
