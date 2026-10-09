import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { AgentStatus } from "../../services/agents";
import type { I18nContextValue } from "../../i18n/index";
import { AgentIcon } from "./AgentIcon";
import {
  AGENT_MENU_MAX_BODY_HEIGHT,
  SectionHeader,
  SelectorChevron,
  hasAgentOptions,
} from "./AgentSelectorParts";

export type AgentSelectorMenuHeaderProps = {
  agents: AgentStatus[];
  agent: string;
  agentColumnRef: MutableRefObject<HTMLDivElement | null>;
  submenuAgent: string | null;
  submenuAgentStatus: AgentStatus | null;
  errorAgent: string | null;
  errorAgentStatus: AgentStatus | null;
  menuBodyHeight: number | null;
  handleSubmenuToggle: (entry: AgentStatus) => void;
  handleAgentRowClick: (entry: AgentStatus) => void;
  setSubmenuAgent: Dispatch<SetStateAction<string | null>>;
  setErrorAgent: Dispatch<SetStateAction<string | null>>;
  setModelSectionExpanded: Dispatch<SetStateAction<boolean>>;
  setModeSectionExpanded: Dispatch<SetStateAction<boolean>>;
  setEffortSectionExpanded: Dispatch<SetStateAction<boolean>>;
  setServiceTierSectionExpanded: Dispatch<SetStateAction<boolean>>;
  t: I18nContextValue["t"];
};

/**
 * 菜单左列的顶部：agent 行（点开子菜单 / 高亮出错的那个）。
 * 2026-10-08 从 AgentSelector.tsx 抽出 —— 它右边那一列（模型/模式/推理档位）
 * 与它只共享 `submenuAgentStatus`，拆开后两列可以各自演进。
 */
export function AgentSelectorMenuHeader({
  agents, agent, agentColumnRef, submenuAgent, submenuAgentStatus, errorAgent, errorAgentStatus,
  menuBodyHeight, handleSubmenuToggle, handleAgentRowClick,
  setSubmenuAgent, setErrorAgent, setModelSectionExpanded, setModeSectionExpanded,
  setEffortSectionExpanded, setServiceTierSectionExpanded, t,
}: AgentSelectorMenuHeaderProps) {
  return (
          <div
            ref={agentColumnRef}
            style={{
              width: "fit-content",
              minWidth: "0",
              maxWidth:
                submenuAgentStatus || errorAgentStatus
                  ? "min(44vw, 180px)"
                  : "min(72vw, 180px)",
              height: menuBodyHeight ? `${menuBodyHeight}px` : "auto",
              maxHeight: `${AGENT_MENU_MAX_BODY_HEIGHT}px`,
              overflowY: "auto",
            }}
          >
            <div
              style={{
                padding: "6px 12px",
                fontSize: "11px",
                fontWeight: 600,
                color: "var(--text-secondary)",
                textTransform: "uppercase",
              }}
            >
              Agent
            </div>
            {agents.map((a) => {
              const hasModelOptions = hasAgentOptions(a);
              const hasError = !a.available && !!a.error;
              const isSelected = a.name === agent;
              const isExpanded = submenuAgent === a.name;
              const isShowingError = errorAgent === a.name;
              return (
                <div
                  key={a.name}
                  style={{
                    minWidth: "100%",
                  }}
                >
                  <div
                    style={{
                      display: "grid",
                      gridTemplateColumns: "20px minmax(0, 1fr) 18px 18px",
                      alignItems: "center",
                      columnGap: "4px",
                      width: "100%",
                      padding: "10px 12px",
                      background:
                        isExpanded || isSelected
                          ? "var(--selection-bg)"
                          : "transparent",
                      opacity: 1,
                    }}
                  >
                    <button
                      type="button"
                      onClick={() => handleAgentRowClick(a)}
                      style={{
                        display: "contents",
                        border: "none",
                        background: "transparent",
                        padding: 0,
                        margin: 0,
                        cursor: "pointer",
                        textAlign: "left",
                      }}
                    >
                      <AgentIcon
                        agentName={a.name}
                        style={{ width: "16px", height: "16px" }}
                      />
                      <span
                        style={{
                          minWidth: 0,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          fontSize: "13px",
                          color:
                            isExpanded || isSelected
                              ? "var(--accent-color)"
                              : "var(--text-primary)",
                          fontWeight: isExpanded || isSelected ? 500 : 400,
                          whiteSpace: "nowrap",
                        }}
                      >
                        {a.name}
                      </span>
                    </button>
                    {hasError ? (
                      <button
                        type="button"
                        aria-label={t("agent.viewErrorInfo", { name: a.name })}
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          setSubmenuAgent(null);
                          setModelSectionExpanded(true);
                          setModeSectionExpanded(false);
                          setEffortSectionExpanded(false);
                          setServiceTierSectionExpanded(false);
                          setErrorAgent((prev) =>
                            prev === a.name ? null : a.name,
                          );
                        }}
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          justifyContent: "center",
                          width: "18px",
                          height: "18px",
                          borderRadius: "999px",
                          border: "1px solid var(--menu-border)",
                          background: isShowingError
                            ? "rgba(217, 119, 6, 0.12)"
                            : "transparent",
                          color: "#d97706",
                          fontSize: "11px",
                          fontWeight: 700,
                          cursor: "pointer",
                          justifySelf: "center",
                        }}
                      >
                        ?
                      </button>
                    ) : (
                      <span
                        aria-hidden="true"
                        style={{ width: "18px", height: "18px" }}
                      />
                    )}
                    {hasModelOptions ? (
                      <button
                        type="button"
                        aria-label={
                          isExpanded
                            ? t("agent.collapseModels", { name: a.name })
                            : t("agent.expandModels", { name: a.name })
                        }
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          handleSubmenuToggle(a);
                        }}
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          justifyContent: "center",
                          width: "18px",
                          height: "18px",
                          borderRadius: "6px",
                          border: "none",
                          background: "transparent",
                          color: isExpanded
                            ? "var(--accent-color)"
                            : "var(--text-secondary)",
                          cursor: "pointer",
                          justifySelf: "center",
                        }}
                      >
                        <SelectorChevron expanded={isExpanded} />
                      </button>
                    ) : (
                      <span
                        aria-hidden="true"
                        style={{ width: "18px", height: "18px" }}
                      />
                    )}
                  </div>
                </div>
              );
            })}
          </div>
  );
}
