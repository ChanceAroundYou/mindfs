import type { TokenUsage } from "./sessionTypes";

/** 状态条需要的 exchange 形状（持久行与瞬时行都满足）。 */
export type SessionStatusExchange = {
  role?: string;
  agent?: string;
  model?: string;
  model_display_name?: string;
  effort?: string;
  fast_service?: string;
  timestamp?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  token_usage?: TokenUsage;
};

/** 会话级实时元信息（运行中由 runtime-meta 写入会话缓存）。 */
export type SessionStatusSession = {
  agent?: string;
  model?: string;
  model_display_name?: string;
  effort?: string;
  fast_service?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
};

export type SessionStatus = {
  agent?: string;
  model?: {
    agent?: string;
    model?: string;
    modelDisplayName?: string;
    effort?: string;
    fastService?: string;
  };
  contextWindow?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  tokenUsage?: TokenUsage;
};

const isAgentRole = (role: string | undefined): boolean => {
  const normalized = `${role || ""}`.toLowerCase();
  return normalized === "agent" || normalized === "assistant";
};

const validContextWindow = (
  contextWindow?:
    | { totalTokens: number; modelContextWindow: number }
    | null,
): contextWindow is { totalTokens: number; modelContextWindow: number } => {
  const total = Math.max(0, Number(contextWindow?.totalTokens || 0));
  const size = Math.max(0, Number(contextWindow?.modelContextWindow || 0));
  return total > 0 && size > 0;
};

/**
 * 会话常驻状态条的数据来源：**每条字段各取最近一次有值的那行**，会话级缓存兜底。
 *
 * 为什么按字段回退而不是只看最后一行：新回合刚开始时，最新的瞬时 agent 行还没有
 * `context_window` / `token_usage`（服务端在 message_done 才回填）。只看最后一行会让
 * 状态条在新回合开头瞬间清空再出现 —— 正是用户报的「运行时老是闪现/消失」。各字段
 * 独立前进：上一轮的值先留着，新的值到了再替换。
 *
 * 各 harness 上报的字段不同：缺失的字段留空，由渲染层逐项隐藏，不伪造 0。
 */
export function selectSessionStatus(
  exchanges: readonly SessionStatusExchange[] | null | undefined,
  session?: SessionStatusSession | null,
): SessionStatus {
  let agent: string | undefined;
  let modelSource: SessionStatusExchange | undefined;
  let contextWindow: SessionStatus["contextWindow"];
  let tokenUsage: TokenUsage | undefined;

  if (Array.isArray(exchanges)) {
    for (let index = exchanges.length - 1; index >= 0; index -= 1) {
      const exchange = exchanges[index];
      if (!exchange) continue;
      if (!agent && isAgentRole(exchange.role)) {
        agent = `${exchange.agent || ""}`.trim() || undefined;
      }
      if (
        !modelSource &&
        (`${exchange.model || ""}`.trim() || `${exchange.model_display_name || ""}`.trim())
      ) {
        modelSource = exchange;
      }
      if (!contextWindow && validContextWindow(exchange.context_window)) {
        contextWindow = {
          totalTokens: Math.max(0, Number(exchange.context_window!.totalTokens || 0)),
          modelContextWindow: Math.max(
            0,
            Number(exchange.context_window!.modelContextWindow || 0),
          ),
        };
      }
      if (!tokenUsage && exchange.token_usage) {
        tokenUsage = exchange.token_usage;
      }
      if (agent && modelSource && contextWindow && tokenUsage) break;
    }
  }

  const resolvedAgent = agent || `${session?.agent || ""}`.trim() || undefined;
  const sessionContextWindow = validContextWindow(session?.context_window)
    ? {
        totalTokens: Math.max(0, Number(session!.context_window!.totalTokens || 0)),
        modelContextWindow: Math.max(
          0,
          Number(session!.context_window!.modelContextWindow || 0),
        ),
      }
    : undefined;

  return {
    agent: resolvedAgent,
    model: {
      agent: resolvedAgent,
      model: modelSource?.model || session?.model,
      modelDisplayName:
        modelSource?.model_display_name || session?.model_display_name,
      effort: modelSource?.effort || session?.effort,
      fastService: modelSource?.fast_service || session?.fast_service,
    },
    contextWindow: contextWindow || sessionContextWindow,
    tokenUsage,
  };
}