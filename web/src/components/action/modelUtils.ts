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

/**
 * 换模型/换 agent 时决定新的 [1M] 状态。
 *
 * 起因：模型下拉里的 id 是**不带后缀的基础 id**（AgentSelector 的 onSelect 传的就是 item.id），
 * 所以「从 of[1m] 切到 os」拿到的 nextModel 是裸的 "os"。旧代码据此把 longContext 判成 false，
 * 于是勾选框里的对勾消失、且 with1MSuffix 不再补后缀 —— 但用户并没有主动关掉它，
 * 实际请求就悄悄退回了普通上下文、触发了压缩。界面与实际不符。
 *
 * 规则：只要**目标仍是 claude**，就把原来的 [1M] 带过去（跨模型、跨同族别名都保留）；
 * 目标不是 claude 时没有 1M 可谈，返回 false —— 此时上层会把勾选框可见地关掉，
 * 而不是留一个勾着却不生效的假象。
 */
export function resolveLongContextOnSwitch(params: {
  nextAgent: string;
  nextModel: string;
  prevLongContext: boolean;
}): boolean {
  if (!params.prevLongContext) return false;
  return isClaudeAgentName(params.nextAgent);
}

/**
 * 换模型/换 agent 时决定新的 thinking effort。
 *
 * 与 1M 同理：旧代码一律回落到目标 agent 的默认值，切一次模型就把用户手选的 effort 抹掉。
 * 现在只在「目标 agent 不支持 effort」或「原值不在新模型的 effort 列表里」时才回默认 ——
 * 前者是能力问题必须降级，后者多半是换了模型族；列表里还有这个值就说明仍然可选，保留。
 */
export function resolveEffortOnSwitch(params: {
  nextAgent: string;
  nextModel: string;
  prevEffort: string;
  defaultEffort: string;
  availableEfforts: string[];
}): string {
  const prev = String(params.prevEffort || "").trim();
  if (!prev) return params.defaultEffort || "";
  if (!params.availableEfforts.includes(prev)) return params.defaultEffort || "";
  return prev;
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
