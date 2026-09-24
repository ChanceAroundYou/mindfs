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

export function parsePlanCommand(input: string): boolean | null {
  const normalized = input.trim().toLowerCase();
  if (normalized === "/plan" || normalized.startsWith("/plan ")) {
    return true;
  }
  return null;
}

export function stripPlanCommandPrefix(input: string): string {
  const trimmed = input.trim();
  if (trimmed.toLowerCase() === "/plan") {
    return "";
  }
  if (trimmed.toLowerCase().startsWith("/plan ")) {
    return trimmed.slice(5).trimStart();
  }
  return trimmed;
}
