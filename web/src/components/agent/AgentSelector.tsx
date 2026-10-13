import React from "react";
import { AgentSelectorTrigger } from "./AgentSelectorTrigger";
import { AgentSelectorErrorBanner } from "./AgentSelectorErrorBanner";
import { AgentSelectorMenuHeader } from "./AgentSelectorMenuHeader";
import {
  AGENT_MENU_MAX_BODY_HEIGHT,
  AgentMenuPortal,
  ModelMenuItem,
  SectionHeader,
  hasAgentOptions,
  parseAgentErrorMessage,
  sectionItemStyle,
} from "./AgentSelectorParts";
import { useAgentSelector } from "./useAgentSelector";
import type { AgentSelectorProps } from "./useAgentSelector";

export function AgentSelector(props: AgentSelectorProps) {
  const {
    compact = false,
    showChevron = false,
    onboardingId,
    ...rest
  } = props;

  const {
    isOpen,
    setIsOpen,
    submenuAgentStatus,
    errorAgentStatus,
    submenuModels,
    filteredSubmenuModels,
    enableModelSearch,
    modelSearch,
    setModelSearch,
    submenuSelectedModel,
    submenuEfforts,
    submenuModes,
    displayedMode,
    submenuSupportsEffort,
    submenuSupportsServiceTier,
    submenuSupportsLongContext,
    longContextEnabled,
    displayedEffort,
    fastModeEnabled,
    buttonTitle,
    menuBodyHeight,
    menuHorizontalOffset,
    viewportMenuPosition,
    dropdownRef,
    menuRef,
    agentColumnRef,
    handleAgentSelect,
    handleAgentRowClick,
    handleSubmenuToggle,
    handleEffortSelect,
    handleServiceTierSelect,
    handleModeSelect,
    handleLongContextToggle,
    handleAgentRestart,
    restartingAgent,
    modelSectionExpanded,
    modeSectionExpanded,
    effortSectionExpanded,
    serviceTierSectionExpanded,
    setSubmenuAgent,
    setErrorAgent,
    setModelSectionExpanded,
    setModeSectionExpanded,
    setEffortSectionExpanded,
    setServiceTierSectionExpanded,
    setMenuBodyHeight,
    setMenuHorizontalOffset,
    setViewportMenuPosition,
    defaultExpandOptions,
    agent,
    model,
    allowDefaultModel,
    viewportMenu,
    menuPlacement,
    t,
  } = useAgentSelector(rest);

  return (
    <div ref={dropdownRef} data-onboarding={onboardingId} style={{ position: "relative" }}>
      <AgentSelectorTrigger
        agent={agent}
        compact={compact}
        showChevron={showChevron}
        warnUnavailable={rest.warnUnavailable}
        buttonTitle={buttonTitle}
        onClick={() => {
          setViewportMenuPosition(null);
          setIsOpen((prev) => {
            const next = !prev;
            if (next) {
              setMenuHorizontalOffset(0);
              const selectedAgent = rest.agents.find((item) => item.name === agent);
              setSubmenuAgent(
                defaultExpandOptions && hasAgentOptions(selectedAgent)
                  ? agent
                  : null,
              );
              setErrorAgent(null);
              setModelSectionExpanded(true);
              setModeSectionExpanded(false);
              setEffortSectionExpanded(false);
              setServiceTierSectionExpanded(false);
              setMenuBodyHeight(null);
            } else {
              setMenuHorizontalOffset(0);
              setSubmenuAgent(null);
              setErrorAgent(null);
              setModelSectionExpanded(true);
              setModeSectionExpanded(false);
              setEffortSectionExpanded(false);
              setServiceTierSectionExpanded(false);
              setMenuBodyHeight(null);
            }
            return next;
          });
        }}
      />

      {isOpen && (
        <AgentMenuPortal enabled={viewportMenu}>
          <div
            ref={menuRef}
            onMouseDown={(e) => e.preventDefault()}
            style={{
              position: viewportMenu ? "fixed" : "absolute",
              ...(viewportMenu
                ? {
                    top: viewportMenuPosition?.top ?? 0,
                    left: viewportMenuPosition?.left ?? 0,
                    visibility: viewportMenuPosition ? "visible" : "hidden",
                  }
                : menuPlacement === "bottom"
                  ? { top: "calc(100% + 8px)", right: 0 }
                  : { bottom: "calc(100% + 8px)", right: 0 }),
              background: "var(--menu-bg)",
              border: "1px solid var(--menu-border)",
              borderRadius: "12px",
              boxShadow: "0 8px 32px rgba(0,0,0,0.15)",
              zIndex: viewportMenu ? 10100 : 1000,
              width: "max-content",
              minWidth: "0",
              maxWidth: "calc(100vw - 16px)",
              boxSizing: "border-box",
              padding: "8px 0",
              display: "flex",
              alignItems: "stretch",
              height: menuBodyHeight ? `${menuBodyHeight + 16}px` : "auto",
              maxHeight: "360px",
              transform:
                viewportMenu || menuHorizontalOffset === 0
                  ? undefined
                  : `translateX(${menuHorizontalOffset}px)`,
            }}
          >
            <AgentSelectorMenuHeader
              agents={rest.agents} agent={agent} agentColumnRef={agentColumnRef} submenuAgent={submenuAgentStatus?.name ?? null}
              submenuAgentStatus={submenuAgentStatus} errorAgent={errorAgentStatus?.name ?? null}
              errorAgentStatus={errorAgentStatus} menuBodyHeight={menuBodyHeight}
              handleSubmenuToggle={handleSubmenuToggle} handleAgentRowClick={handleAgentRowClick}
              setSubmenuAgent={setSubmenuAgent} setErrorAgent={setErrorAgent}
              setModelSectionExpanded={setModelSectionExpanded} setModeSectionExpanded={setModeSectionExpanded}
              setEffortSectionExpanded={setEffortSectionExpanded}
              setServiceTierSectionExpanded={setServiceTierSectionExpanded} t={t}
            />

            <div
              style={{
                width:
                  submenuAgentStatus || errorAgentStatus ? "fit-content" : "0",
                minWidth: submenuAgentStatus || errorAgentStatus ? "0" : "0",
                maxWidth:
                  submenuAgentStatus || errorAgentStatus
                    ? "min(40vw, 180px)"
                    : "0",
                borderLeft:
                  submenuAgentStatus || errorAgentStatus
                    ? "1px solid var(--menu-divider)"
                    : "none",
                height: menuBodyHeight ? `${menuBodyHeight}px` : "auto",
                maxHeight: `${AGENT_MENU_MAX_BODY_HEIGHT}px`,
                overflowY: "auto",
                overflowX: "hidden",
                transition: "width 0.16s ease, border-left-color 0.16s ease",
                boxSizing: "border-box",
              }}
            >
              {errorAgentStatus &&
              parseAgentErrorMessage(errorAgentStatus.error) ? (
                <AgentSelectorErrorBanner
                  errorAgentStatus={errorAgentStatus} handleAgentRestart={handleAgentRestart}
                  onAgentRestart={rest.onAgentRestart} restartingAgent={restartingAgent} t={t}
                />
              ) : submenuAgentStatus ? (
                <>
                  <SectionHeader
                    title={t("agent.model")}
                    expanded={modelSectionExpanded}
                    onToggle={() => setModelSectionExpanded((prev) => !prev)}
                    value={
                      allowDefaultModel && submenuAgentStatus.name === agent && !model
                        ? t("agent.defaultModel")
                        : submenuSelectedModel?.id || undefined
                    }
                  />
                  {modelSectionExpanded ? (
                    <>
                      {enableModelSearch ? (
                        <div
                          style={{
                            padding: "2px 10px",
                            position: "sticky",
                            top: 0,
                            zIndex: 1,
                            background: "var(--menu-bg)",
                          }}
                        >
                          <input
                            type="text"
                            value={modelSearch}
                            onChange={(event) =>
                              setModelSearch(event.target.value)
                            }
                            placeholder={t("agent.modelSearchPlaceholder")}
                            style={{
                              display: "block",
                              width: "100%",
                              boxSizing: "border-box",
                              height: "26px",
                              padding: "0 10px",
                              border: "1px solid var(--menu-divider)",
                              borderRadius: "8px",
                              background: "transparent",
                              color: "var(--text-primary)",
                              fontSize: "12px",
                              outline: "none",
                            }}
                          />
                        </div>
                      ) : null}
                      {allowDefaultModel &&
                      filteredSubmenuModels.length > 0 ? (
                        <button
                          type="button"
                          onClick={() => handleAgentSelect(submenuAgentStatus.name, "")}
                          style={sectionItemStyle(
                            submenuAgentStatus.name === agent && !model,
                            false,
                          )}
                        >
                          <span style={{ fontSize: "13px", fontWeight: 500 }}>
                            {t("agent.defaultModel")}
                          </span>
                          <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
                            {t("agent.defaultModelDescription")}
                          </span>
                        </button>
                      ) : null}
                      {filteredSubmenuModels.length === 0 ? (
                        <div
                          style={{
                            padding: "10px 12px",
                            fontSize: "12px",
                            color: "var(--text-secondary)",
                          }}
                        >
                          {t("agent.modelNoMatch")}
                        </div>
                      ) : (
                        filteredSubmenuModels.map((item, index) => (
                          <ModelMenuItem
                            key={item.id}
                            item={item}
                            selected={
                              submenuAgentStatus.name === agent &&
                              !!model &&
                              item.id === (submenuSelectedModel?.id || "")
                            }
                            topBorder={
                              (allowDefaultModel &&
                                filteredSubmenuModels.length > 0) ||
                              index > 0
                            }
                            onSelect={handleAgentSelect}
                            agentName={submenuAgentStatus.name}
                          />
                        ))
                      )}
                    </>
                  ) : null}
                  {submenuModes.length > 0 ? (
                    <>
                      <SectionHeader
                        title={t("agent.mode")}
                        expanded={modeSectionExpanded}
                        onToggle={() => setModeSectionExpanded((prev) => !prev)}
                        topBorder={
                          modelSectionExpanded ||
                          submenuModels.length > 0 ||
                          !!submenuSelectedModel?.id
                        }
                        value={displayedMode || undefined}
                      />
                      {modeSectionExpanded ? (
                        <>
                          {submenuModes.map((item, index) => (
                            <button
                              key={item.id}
                              type="button"
                              onClick={() => handleModeSelect(item.id)}
                              style={sectionItemStyle(
                                item.id === displayedMode,
                                index > 0,
                              )}
                              title={item.description || item.id}
                            >
                              <span style={{ fontSize: "13px", fontWeight: 500 }}>
                                {item.name || item.id}
                              </span>
                              {item.description ? (
                                <span
                                  style={{
                                    fontSize: "11px",
                                    color: "var(--text-secondary)",
                                    whiteSpace: "normal",
                                    overflowWrap: "anywhere",
                                    wordBreak: "break-word",
                                  }}
                                >
                                  {item.description}
                                </span>
                              ) : null}
                            </button>
                          ))}
                        </>
                      ) : null}
                    </>
                  ) : null}
                  {submenuSupportsEffort ? (
                    <>
                      <SectionHeader
                        title={t("agent.effort")}
                        expanded={effortSectionExpanded}
                        onToggle={() => setEffortSectionExpanded((prev) => !prev)}
                        topBorder={
                          modelSectionExpanded ||
                          submenuModels.length > 0 ||
                          !!submenuSelectedModel?.id ||
                          submenuModes.length > 0
                        }
                        value={displayedEffort}
                      />
                      {effortSectionExpanded ? (
                        <>
                          {submenuEfforts.map((item, index) => (
                            <button
                              key={item}
                              type="button"
                              onClick={() => handleEffortSelect(item)}
                              style={sectionItemStyle(
                                item === displayedEffort.toLowerCase(),
                                index > 0,
                              )}
                            >
                              <span
                                style={{
                                  fontSize: "13px",
                                  fontWeight: 500,
                                  textTransform: "capitalize",
                                }}
                              >
                                {item}
                              </span>
                            </button>
                          ))}
                        </>
                      ) : null}
                    </>
                  ) : null}
                  {submenuSupportsLongContext ? (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        handleLongContextToggle();
                      }}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "space-between",
                        gap: "12px",
                        width: "100%",
                        padding: "10px 12px",
                        border: "none",
                        borderTop:
                          modelSectionExpanded ||
                          submenuModels.length > 0 ||
                          !!submenuSelectedModel?.id ||
                          submenuModes.length > 0 ||
                          submenuSupportsEffort
                            ? "1px solid var(--menu-divider)"
                            : "none",
                        background: "transparent",
                        cursor: "pointer",
                        textAlign: "left",
                      }}
                    >
                      <span style={{ fontSize: "11px", fontWeight: 700, letterSpacing: "0.08em", textTransform: "uppercase", color: "var(--text-secondary)" }}>
                        {t("agent.longContext")}
                      </span>
                      <span
                        aria-hidden="true"
                        style={{
                          flex: "0 0 auto",
                          width: "36px",
                          height: "20px",
                          borderRadius: "999px",
                          padding: "2px",
                          boxSizing: "border-box",
                          background: longContextEnabled ? "var(--accent-color)" : "rgba(0,0,0,0.18)",
                          display: "inline-flex",
                          alignItems: "center",
                          justifyContent: longContextEnabled ? "flex-end" : "flex-start",
                          transition: "background 0.18s ease",
                        }}
                      >
                        <span
                          style={{
                            width: "16px",
                            height: "16px",
                            borderRadius: "50%",
                            background: "#fff",
                            boxShadow: "0 1px 3px rgba(0,0,0,0.2)",
                            transition: "transform 0.18s ease",
                          }}
                        />
                      </span>
                    </button>
                  ) : null}
                  {submenuSupportsServiceTier ? (
                    <>
                      <SectionHeader
                        title={t("agent.fastMode")}
                        expanded={serviceTierSectionExpanded}
                        onToggle={() =>
                          setServiceTierSectionExpanded((prev) => !prev)
                        }
                        topBorder={
                          modelSectionExpanded ||
                          submenuModels.length > 0 ||
                          !!submenuSelectedModel?.id ||
                          submenuModes.length > 0 ||
                          submenuSupportsEffort
                        }
                        value={fastModeEnabled ? t("agent.enabled") : t("agent.disabled")}
                      />
                      {serviceTierSectionExpanded ? (
                        <>
                          {(["off", "on"] as const).map((item, index) => (
                            <button
                              key={item}
                              type="button"
                              onClick={() => handleServiceTierSelect(item)}
                              style={sectionItemStyle(
                                (item === "on") === fastModeEnabled,
                                index > 0,
                              )}
                            >
                              <span
                                style={{
                                  fontSize: "13px",
                                  fontWeight: 500,
                                }}
                              >
                                {item === "on" ? t("agent.enabled") : t("agent.disabled")}
                              </span>
                            </button>
                          ))}
                        </>
                      ) : null}
                    </>
                  ) : null}
                </>
              ) : null}
            </div>
          </div>
        </AgentMenuPortal>
      )}
    </div>
  );
}
