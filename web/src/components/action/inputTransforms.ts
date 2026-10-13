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
