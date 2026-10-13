// Stateless SessionService helpers. Kept separate so the service facade retains
// the public API while identifier generation remains independently testable.

export function generateClientId(): string {
  return `web-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function createRequestId(prefix = "msg"): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
