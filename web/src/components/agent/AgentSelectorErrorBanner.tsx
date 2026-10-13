import type { AgentStatus } from "../../services/agents";
import type { I18nContextValue } from "../../i18n/index";
import { parseAgentErrorDetails, parseAgentErrorMessage } from "./AgentSelectorParts";

export type AgentSelectorErrorBannerProps = {
  errorAgentStatus: AgentStatus;
  handleAgentRestart: (targetAgent: string) => void | Promise<void>;
  onAgentRestart?: (agent: string) => void | Promise<void>;
  restartingAgent: string | null;
  t: I18nContextValue["t"];
};

/**
 * 菜单里的 agent 报错横幅 + 「重启该 agent」按钮（2026-10-08 从 AgentSelector.tsx 抽出）。
 * 120 行 JSX 只闭包 5 个符号，其中两个是纯展示态 —— 它本来就是一整块 UI，
 * 留在宿主里只会把左右两列的逻辑搅在一起。
 */
export function AgentSelectorErrorBanner({
  errorAgentStatus, handleAgentRestart, onAgentRestart, restartingAgent, t,
}: AgentSelectorErrorBannerProps) {
  return (
              <div
                style={{
                  width: "100%",
                  minWidth: 0,
                  padding: "12px",
                  boxSizing: "border-box",
                }}
              >
                <style>{`
                  @keyframes agent-refresh-spin {
                    from { transform: rotate(0deg); }
                    to { transform: rotate(360deg); }
                  }
                `}</style>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: "8px",
                    marginBottom: "8px",
                  }}
                >
                  <div
                    style={{
                      fontSize: "11px",
                      fontWeight: 600,
                      color: "#d97706",
                      textTransform: "uppercase",
                    }}
                  >
                    {t("agent.errorInfo")}
                  </div>
                  {onAgentRestart ? (
                    <button
                      type="button"
                      aria-label={t("agent.restart", { name: errorAgentStatus.name })}
                      title={t("agent.restartAgent")}
                      disabled={restartingAgent === errorAgentStatus.name}
                      onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        void handleAgentRestart(errorAgentStatus.name);
                      }}
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        width: "22px",
                        height: "22px",
                        borderRadius: "7px",
                        border: "none",
                        background: "transparent",
                        color: "#d97706",
                        cursor:
                          restartingAgent === errorAgentStatus.name
                            ? "default"
                            : "pointer",
                        opacity:
                          restartingAgent === errorAgentStatus.name ? 0.62 : 1,
                        padding: 0,
                        flex: "0 0 auto",
                      }}
                    >
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="1em"
                        height="1em"
                        viewBox="0 0 24 24"
                        aria-hidden="true"
                        style={{
                          transformOrigin: "50% 50%",
                          animation:
                            restartingAgent === errorAgentStatus.name
                              ? "agent-refresh-spin 0.9s linear infinite"
                              : undefined,
                        }}
                      >
                        <path d="M0 0h24v24H0z" fill="none" />
                        <path
                          fill="currentColor"
                          d="M12 20q-3.35 0-5.675-2.325T4 12t2.325-5.675T12 4q1.725 0 3.3.712T18 6.75V5q0-.425.288-.712T19 4t.713.288T20 5v5q0 .425-.288.713T19 11h-5q-.425 0-.712-.288T13 10t.288-.712T14 9h3.2q-.8-1.4-2.187-2.2T12 6Q9.5 6 7.75 7.75T6 12t1.75 4.25T12 18q1.7 0 3.113-.862t2.187-2.313q.2-.35.563-.487t.737-.013q.4.125.575.525t-.025.75q-1.025 2-2.925 3.2T12 20"
                        />
                      </svg>
                    </button>
                  ) : null}
                </div>
                <div
                  style={{
                    padding: "10px 12px",
                    borderRadius: "10px",
                    background: "rgba(217, 119, 6, 0.08)",
                    border: "1px solid rgba(217, 119, 6, 0.18)",
                    color: "var(--text-primary)",
                    fontSize: "12px",
                    lineHeight: 1.5,
                    whiteSpace: "normal",
                    overflowWrap: "anywhere",
                    wordBreak: "break-word",
                  }}
                >
                  {parseAgentErrorMessage(errorAgentStatus.error)}
                </div>
                {parseAgentErrorDetails(errorAgentStatus.error).map(
                  (detail) => (
                    <div
                      key={detail}
                      style={{
                        marginTop: "8px",
                        padding: "10px 12px",
                        borderRadius: "10px",
                        background: "rgba(0, 0, 0, 0.03)",
                        border: "1px solid var(--menu-divider)",
                        color: "var(--text-secondary)",
                        fontSize: "11px",
                        lineHeight: 1.5,
                        whiteSpace: "normal",
                        overflowWrap: "anywhere",
                        wordBreak: "break-word",
                      }}
                    >
                      {detail}
                    </div>
                  ),
                )}
              </div>
  );
}
