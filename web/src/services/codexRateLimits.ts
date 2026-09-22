import { protectedJSON } from "./api";
import { appPath, appendQuery } from "./base";

export type CodexRateLimitWindow = {
  used_percent: number;
  window_duration_mins?: number;
  resets_at?: number;
};

export type CodexRateLimitResetCredit = {
  id: string;
  reset_type?: string;
  status?: string;
  granted_at?: number;
  expires_at?: number;
  title?: string;
  description?: string;
};

export type CodexRateLimitStatus = {
  uses_chatgpt_plan: boolean;
  weekly?: CodexRateLimitWindow;
  reset_credits?: {
    available_count: number;
    credits?: CodexRateLimitResetCredit[];
  };
};

export type ConsumeCodexRateLimitResetResult = {
  outcome: "reset" | "nothingToReset" | "noCredit" | "alreadyRedeemed" | string;
  status: CodexRateLimitStatus;
};

export async function fetchCodexRateLimits(agent = "codex", nodeId?: string): Promise<CodexRateLimitStatus> {
  const params = new URLSearchParams({ agent });
  return protectedJSON<CodexRateLimitStatus>(
    appendQuery(appPath("/api/agents/codex/rate-limits", nodeId), params),
  );
}

export async function consumeCodexRateLimitReset(
  idempotencyKey: string,
  creditId?: string,
  agent = "codex",
  nodeId?: string,
): Promise<ConsumeCodexRateLimitResetResult> {
  return protectedJSON<ConsumeCodexRateLimitResetResult>(
    appPath("/api/agents/codex/rate-limit-reset", nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        agent,
        idempotency_key: idempotencyKey,
        ...(creditId ? { credit_id: creditId } : {}),
      }),
    },
  );
}
