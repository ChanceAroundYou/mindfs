export function normalizeSessionLockKey(value: unknown): string | null {
  const key = typeof value === "string" ? value.trim() : "";
  return key || null;
}

export function resolveLockedSessionKey(value: unknown): string | undefined {
  const key = normalizeSessionLockKey(value);
  return key && !key.startsWith("pending-") ? key : undefined;
}

export function shouldResetSessionLockForRootChange(
  currentRoot: string | null | undefined,
  targetRoot: string | null | undefined,
): boolean {
  const current = String(currentRoot || "").trim();
  const target = String(targetRoot || "").trim();
  return !!current && !!target && current !== target;
}
