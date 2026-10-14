import type { AgentStatus } from "../../shared/agentTypes";

export function strip1MSuffix(model: string): string {
  const trimmed = String(model || "").trim();
  return trimmed.toLowerCase().endsWith("[1m]") ? trimmed.slice(0, -4).trim() : trimmed;
}

export function isClaudeAliasModelName(model: string): boolean {
  const base = strip1MSuffix(model).trim().toLowerCase();
  return base === "fable" || base === "opus" || base === "sonnet" || base === "haiku" || base === "of" || base === "op" || base === "os" || base === "ok" || base === "default";
}

export function claudeModelBase(model: string): string {
  if (!isClaudeAliasModelName(model)) return strip1MSuffix(model);
  const base = strip1MSuffix(model);
  switch (base.toLowerCase()) {
    case "fable": return "of";
    case "opus": return "op";
    case "sonnet": return "os";
    case "haiku": return "ok";
    case "of": case "op": case "os": case "ok": return base.toLowerCase();
    default: return base;
  }
}

export function hasAgentOptions(agent?: AgentStatus): boolean {
  const isClaude = String(agent?.name || "").trim().toLowerCase() === "claude";
  return !!(
    agent &&
    ((agent.models?.length ?? 0) > 0 ||
      (agent.modes?.length ?? 0) > 0 ||
      (agent.efforts?.length ?? 0) > 0 ||
      agent.supports_fast_service ||
      isClaude)
  );
}

export function parseAgentErrorMessage(error?: string): string {
  const raw = String(error || "").trim();
  if (!raw) {
    return "";
  }

  try {
    const parsed = JSON.parse(raw) as {
      message?: unknown;
    };
    return typeof parsed.message === "string" && parsed.message.trim()
      ? parsed.message.trim()
      : raw;
  } catch {
    return raw;
  }
}

export function parseAgentErrorDetails(error?: string): string[] {
  const raw = String(error || "").trim();
  if (!raw) {
    return [];
  }

  try {
    const parsed = JSON.parse(raw) as {
      data?: unknown;
    };
    if (parsed.data === undefined) {
      return [];
    }

    if (Array.isArray(parsed.data)) {
      return parsed.data.map((item) => String(item)).filter(Boolean);
    }

    if (parsed.data && typeof parsed.data === "object") {
      if (
        Array.isArray((parsed.data as { authMethods?: unknown }).authMethods)
      ) {
        return (
          parsed.data as {
            authMethods: Array<{ name?: unknown; description?: unknown }>;
          }
        ).authMethods
          .map((item) => {
            const name = typeof item?.name === "string" ? item.name.trim() : "";
            const description =
              typeof item?.description === "string"
                ? item.description.trim()
                : "";
            if (name && description) {
              return `${name}: ${description}`;
            }
            return name || description;
          })
          .filter(Boolean);
      }
      return Object.entries(parsed.data as Record<string, unknown>).map(
        ([key, value]) => {
          if (typeof value === "string") {
            return `${key}: ${value}`;
          }
          return `${key}: ${JSON.stringify(value)}`;
        },
      );
    }

    return [String(parsed.data)];
  } catch {
    return [];
  }
}
