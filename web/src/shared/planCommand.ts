/**
 * Plan mode's text prefix is a shared message contract. Keep detection,
 * stripping, and construction in this UI independent module.
 */
export const PLAN_COMMAND = "/plan";

/** Whether the input requests plan mode through the first-message prefix. */
export function isPlanCommand(input: string): boolean {
  const normalized = input.trim().toLowerCase();
  return normalized === PLAN_COMMAND || normalized.startsWith(`${PLAN_COMMAND} `);
}

/** Compatibility helper: true for a plan command and null otherwise. */
export function parsePlanCommand(input: string): boolean | null {
  return isPlanCommand(input) ? true : null;
}

/** Remove the plan prefix, leaving non-plan input trimmed as before. */
export function stripPlanCommandPrefix(input: string): string {
  const trimmed = input.trim();
  if (!isPlanCommand(trimmed)) return trimmed;
  return trimmed.slice(PLAN_COMMAND.length).trimStart();
}

/** Add the prefix when a locally selected plan mode starts a new session. */
export function withPlanPrefix(input: string): string {
  return `${PLAN_COMMAND} ${input}`;
}
