import { type ActiveToken } from "../editor/tokenEditorUtils";

export function replaceActiveTokenText(input: string, activeToken: ActiveToken | null, value: string): string {
  if (!activeToken) return input;
  const trigger = activeToken.type === "file" ? "@" : activeToken.type === "prompt" ? "#" : activeToken.type === "slash" ? "/" : "";
  if (!trigger) return value;
  const needle = `${trigger}${activeToken.query}`;
  const index = input.lastIndexOf(needle);
  if (index < 0) {
    return `${input}${value} `;
  }
  return `${input.slice(0, index)}${value} ${input.slice(index + needle.length)}`;
}

/**
 * plan 模式的正文前缀。**本模块是唯一知道这个字面量的地方** —— 检测、剥离、拼装三者同源。
 *
 * 此前这个前缀被 5 处各自认识一遍（本文件两个函数各解析一次、`App.tsx` 内联第三次判断、
 * 发送前又内联拼回前缀），改格式必漏（冲突⑮b）。`session.plan_mode.set` 那条**二进制**通道
 * 与本前缀是并存的两种入口（后者用于「还没有 sessionKey 的新会话」，前缀随首条消息一起建会话），
 * 这不是重复 —— 重复的是「同一个文本前缀被多处各自解析」。
 */
export const PLAN_COMMAND = "/plan";

/** 这条输入是不是「请求进入 plan 模式」。 */
export function isPlanCommand(input: string): boolean {
  const normalized = input.trim().toLowerCase();
  return normalized === PLAN_COMMAND || normalized.startsWith(`${PLAN_COMMAND} `);
}

/** 兼容旧签名：是 plan 命令返回 true，否则 null。 */
export function parsePlanCommand(input: string): boolean | null {
  return isPlanCommand(input) ? true : null;
}

/** 剥掉前缀后的正文（不是 plan 命令时原样返回）。 */
export function stripPlanCommandPrefix(input: string): string {
  const trimmed = input.trim();
  if (!isPlanCommand(trimmed)) {
    return trimmed;
  }
  return trimmed.slice(PLAN_COMMAND.length).trimStart();
}

/** 发送前把前缀拼回正文（用于「本地记住要进 plan，但等首条消息一起发」）。 */
export function withPlanPrefix(input: string): string {
  return `${PLAN_COMMAND} ${input}`;
}
