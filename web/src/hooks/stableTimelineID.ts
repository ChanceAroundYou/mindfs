function hashText(input: string): string {
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

export function stableTimelineID(
  prefix: string,
  index: number,
  content: string,
  timestamp?: string,
  agent?: string,
): string {
  return `${prefix}:${index}:${timestamp || ""}:${agent || ""}:${hashText(content)}`;
}
