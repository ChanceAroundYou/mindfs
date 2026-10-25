// Shared session contracts used by the UI and session service implementation.
//
// 全是**纯类型**：运行期被擦除，所以这次搬移对 VM 测试、source-map 守卫、e2e
// 零影响。留在 session.ts 的只有 `SESSION_WINDOW_SIZE`（G-AT 锚点要求那句话
// 逐字在 session.ts 里）与 `PENDING_STREAM_LIMIT`（属于 WS 传输的运行时行为）。
//
// SessionEventHandler / SessionServiceEvent / PendingMessage are implementation-facing contracts;
// the public services/session facade continues to export only its existing API surface.

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

// SessionAuxFlags 是会话行的内容徽标数据源（G-AD），与后端 session.SessionAuxFlags
// 同名同义：ask_user/plan/todo/task/error。两处 SessionItem（appSession.ts 与
// components/session/sessionListTypes.ts）共用这一份，不许各自再定义一遍。
export type SessionAuxFlags = {
  ask_user_waiting?: boolean;
  has_plan?: boolean;
  has_todos?: boolean;
  has_task?: boolean;
  session_error?: string;
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
  /**
   * work_dir 是这个会话运行时的工作目录（cwd），空 = 项目主 checkout。
   * 与 related_worktree 是两件事：后者表达「属于哪个工作树」（任务收尾会清它、
   * 列表按它显示工作树徽标），前者表达「在哪个目录里干活」，由标题栏菜单的
   * 「修改目录」写入。两者同时存在时以 work_dir 为准。
   */
  work_dir?: string;
  // worktree_missing 是派生状态（目录被删了）：后端按需计算、不落库（G-AD 列表透传）。
  worktree_missing?: boolean;
  /**
   * worktree_active 是绿色 worktree 标签的**唯一**判据：这个会话真的绑在一个活着的
   * linked worktree 上（路径在 + 目录还在 + `.git` 是文件）。派生状态、不落库。
   *
   * 前端 stat 不了文件系统，所以判据只能由后端给。原先前端拿 related_worktree.path
   * 非空 + !worktree_missing 自己判，主 checkout 的目录当然一直在 —— 2026-10-16 实测
   * 14 条归属里 13 条误亮。别退回那个判据。
   */
  worktree_active?: boolean;
  // aux_flags 是会话行的内容徽标数据源（G-AD）：ask_user/plan/todo/task/error，
  // toSessionItem 从列表 payload 透传、session.meta.updated 按 key 增量合并。
  aux_flags?: SessionAuxFlags;
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
  /**
   * 允许复用这个时间窗内**已结算**的同参结果（毫秒）。仅「打开会话」这类
   * 明确接受快照的调用方该传（CUSTOM(G-JU)）；默认 0 = 每次重新发。
   */
  reuseWithinMs?: number;
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
