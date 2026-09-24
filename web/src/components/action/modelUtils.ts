import { type AgentStatus } from "../../services/agents";

export function isClaudeAgentName(name?: string | null) {
  return String(name || "").trim().toLowerCase() === "claude";
}

export function isClaudeAliasModel(model: string): boolean {
  const base = strip1MSuffix(model);
  const lower = String(base || "").trim().toLowerCase();
  return lower === "fable" || lower === "opus" || lower === "sonnet" || lower === "haiku" || lower === "of" || lower === "op" || lower === "os" || lower === "ok" || lower === "default";
}

export function resolveClaudeBaseAlias(base: string): string {
  const trimmed = String(base || "").trim();
  if (!trimmed) return "";
  const lower = trimmed.toLowerCase();
  if (lower === "fable") return "of";
  if (lower === "opus") return "op";
  if (lower === "sonnet") return "os";
  if (lower === "haiku") return "ok";
  if (lower === "of" || lower === "op" || lower === "os" || lower === "ok") return lower;
  return trimmed;
}

export function strip1MSuffix(model: string): string {
  const trimmed = String(model || "").trim();
  return trimmed.toLowerCase().endsWith("[1m]") ? trimmed.slice(0, -4).trim() : trimmed;
}

export function has1MSuffix(model: string): boolean {
  return String(model || "").trim().toLowerCase().endsWith("[1m]");
}

export function with1MSuffix(model: string, enabled: boolean): string {
  const base = strip1MSuffix(model);
  if (!base) return "";
  if (isClaudeAliasModel(base)) {
    const alias = resolveClaudeBaseAlias(base);
    if (!alias) return "";
    return enabled ? `${alias}[1m]` : alias;
  }
  return enabled ? `${base}[1m]` : base;
}

export function modelBaseForAgent(agentName: string | undefined, model: string): string {
  const base = strip1MSuffix(model);
  if (!isClaudeAgentName(agentName) || !isClaudeAliasModel(base)) return base;
  return resolveClaudeBaseAlias(base);
}

export function getAgentDefaults(agent?: AgentStatus | null) {
  return {
    model: agent?.default_model_id || agent?.current_model_id || "",
    effort: agent?.default_effort || "",
    fastService: (agent?.default_fast_service || "") as "" | "on" | "off",
  } as const;
}

export function buildPendingAttachment(file: File): {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
} {
  const isImage = file.type.startsWith("image/");
  const fallbackExt = file.type.split("/")[1] || "png";
  const fileName = file.name || `pasted-image-${Date.now()}.${fallbackExt}`;
  const normalizedFile = file.name
    ? file
    : new File([file], fileName, {
      type: file.type || "image/png",
      lastModified: file.lastModified || Date.now(),
    });
  return {
    id: `${normalizedFile.name}-${normalizedFile.size}-${normalizedFile.lastModified}-${Math.random().toString(36).slice(2, 8)}`,
    file: normalizedFile,
    isImage,
    previewUrl: isImage ? URL.createObjectURL(normalizedFile) : undefined,
  };
}
