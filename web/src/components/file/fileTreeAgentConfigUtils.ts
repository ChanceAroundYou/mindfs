import type { I18nContextValue } from "../../i18n";
import type { AgentAPIProviderTestResult } from "../../services/agentConfig";
export function formatProviderTestResult(result: AgentAPIProviderTestResult, t: I18nContextValue["t"]): string {
  const latency = t("agentConfig.testModelLatency", { ms: result.latency_ms });
  return result.success ? `✓ ${latency}${result.response ? ` ${result.response}` : ""}` : `✗ ${latency} ${result.error || t("agentConfig.testModelFailed")}`;
}
export function isAgentConfigBackupConflict(error: unknown): boolean {
  const e = error as { status?: unknown; message?: unknown; payload?: { error?: unknown; message?: unknown } } | null;
  if (!e) return false;
  return (typeof e.status === "number" && e.status === 409) || String(e.payload?.error || e.payload?.message || e.message || "") === "backup already exists";
}
