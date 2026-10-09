import React, {
  useState,
  useRef,
  useEffect,
  useLayoutEffect,
  useCallback,
  useMemo,
} from "react";
import { createPortal } from "react-dom";
import { AgentIcon } from "./AgentIcon";
import { AgentSelectorErrorBanner } from "./AgentSelectorErrorBanner";
import { AgentSelectorMenuHeader } from "./AgentSelectorMenuHeader";
import type { AgentStatus } from "../../services/agents";
import { filterAgentModels } from "../../shared/modelFiltering";
import { useI18n } from "../../i18n/index";
import {
  AGENT_MENU_MAX_BODY_HEIGHT,
  AGENT_MENU_MIN_BODY_HEIGHT,
  AGENT_MODEL_SEARCH_THRESHOLD,
  AgentMenuPortal,
  ModelMenuItem,
  SectionHeader,
  SelectorChevron,
  claudeModelBase,
  hasAgentOptions,
  isClaudeAliasModelName,
  parseAgentErrorDetails,
  parseAgentErrorMessage,
  sectionItemStyle,
  strip1MSuffix,
} from "./AgentSelectorParts";

type AgentSelectorProps = {
  agent: string;
  model?: string;
  mode?: string;
  effort?: string;
  longContext?: boolean;
  fastService?: "" | "on" | "off";
  agents: AgentStatus[];
  onAgentChange: (agent: string, model?: string) => void;
  onModeChange?: (mode?: string) => void;
  onEffortChange?: (effort?: string) => void;
  onLongContextChange?: (enabled: boolean) => void;
  onFastServiceChange?: (fastService?: "" | "on" | "off") => void;
  onAgentRestart?: (agent: string) => void | Promise<void>;
  compact?: boolean;
  warnUnavailable?: boolean;
  menuPlacement?: "top" | "bottom";
  showChevron?: boolean;
  defaultExpandOptions?: boolean;
  onboardingId?: string;
  viewportMenu?: boolean;
  allowDefaultModel?: boolean;
  onOpenChange?: (open: boolean) => void;
};



export function AgentSelector({
  agent,
  model = "",
  mode = "",
  effort = "",
  longContext = false,
  fastService = "",
  agents,
  onAgentChange,
  onModeChange,
  onEffortChange,
  onLongContextChange,
  onFastServiceChange,
  onAgentRestart,
  compact = false,
  warnUnavailable = false,
  menuPlacement = "top",
  showChevron = false,
  defaultExpandOptions = false,
  onboardingId,
  viewportMenu = false,
  allowDefaultModel = false,
  onOpenChange,
}: AgentSelectorProps) {
  const { t } = useI18n();
  const [isOpen, setIsOpen] = useState(false);
  const [submenuAgent, setSubmenuAgent] = useState<string | null>(null);
  const [errorAgent, setErrorAgent] = useState<string | null>(null);
  const [modelSectionExpanded, setModelSectionExpanded] = useState(true);
  const [modeSectionExpanded, setModeSectionExpanded] = useState(false);
  const [effortSectionExpanded, setEffortSectionExpanded] = useState(false);
  const [serviceTierSectionExpanded, setServiceTierSectionExpanded] =
    useState(false);
  const [restartingAgent, setRestartingAgent] = useState<string | null>(null);
  const [menuBodyHeight, setMenuBodyHeight] = useState<number | null>(null);
  const [modelSearch, setModelSearch] = useState("");
  const [menuHorizontalOffset, setMenuHorizontalOffset] = useState(0);
  const [positionTick, setPositionTick] = useState(0);
  const [viewportMenuPosition, setViewportMenuPosition] = useState<{
    top: number;
    left: number;
  } | null>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const agentColumnRef = useRef<HTMLDivElement>(null);
  const submenuAgentStatus = useMemo(
    () => agents.find((item) => item.name === submenuAgent) ?? null,
    [agents, submenuAgent],
  );
  const errorAgentStatus = useMemo(
    () => agents.find((item) => item.name === errorAgent) ?? null,
    [agents, errorAgent],
  );
  const submenuModels = useMemo(
    () => submenuAgentStatus?.models ?? [],
    [submenuAgentStatus],
  );
  const enableModelSearch = submenuModels.length > AGENT_MODEL_SEARCH_THRESHOLD;
  const filteredSubmenuModels = useMemo(
    () => filterAgentModels(submenuModels, modelSearch),
    [submenuModels, modelSearch],
  );
  useEffect(() => {
    if (!isOpen || !submenuAgent) {
      setModelSearch("");
    }
  }, [isOpen, submenuAgent]);
  const submenuSelectedModel = useMemo(() => {
    if (!submenuAgentStatus) return null;
    const fallbackModel =
      submenuAgentStatus.default_model_id ||
      submenuAgentStatus.current_model_id ||
      "";
    const targetModel =
      submenuAgentStatus.name === agent
        ? model || fallbackModel
        : fallbackModel;
    const models = submenuAgentStatus.models ?? [];
    const exact = models.find((item) => item.id === targetModel);
    if (exact) return exact;
    // Generic [1m]: probe advertises base ids without suffix, UI may hold base[1m].
    // Alias family uses canonical alias bases, otherwise stripped bases.
    if (isClaudeAliasModelName(targetModel)) {
      return models.find((item) => isClaudeAliasModelName(item.id) && claudeModelBase(item.id) === claudeModelBase(targetModel)) ?? null;
    }
    return models.find((item) => strip1MSuffix(item.id) === strip1MSuffix(targetModel)) ?? null;
  }, [submenuAgentStatus, agent, model]);
  const submenuEfforts = useMemo(
    () => submenuSelectedModel?.efforts ?? submenuAgentStatus?.efforts ?? [],
    [submenuAgentStatus, submenuSelectedModel],
  );
  const submenuModes = useMemo(
    () => submenuAgentStatus?.modes ?? [],
    [submenuAgentStatus],
  );
  const displayedMode = useMemo(() => {
    if (!submenuAgentStatus) return "";
    const fallbackMode = submenuAgentStatus.current_mode_id || "";
    return submenuAgentStatus.name === agent
      ? mode || fallbackMode
      : fallbackMode;
  }, [submenuAgentStatus, agent, mode]);
  const submenuIsCodex = submenuAgentStatus?.name === "codex";
  const submenuSupportsEffort = useMemo(
    () => submenuEfforts.length > 0 && !!submenuSelectedModel?.supportEffort,
    [submenuEfforts, submenuSelectedModel],
  );
  const submenuSupportsServiceTier =
    !!submenuAgentStatus?.supports_fast_service;
  const submenuSupportsLongContext =
    String(submenuAgentStatus?.name || "").trim().toLowerCase() === "claude";
  const longContextEnabled =
    submenuAgentStatus?.name === agent ? !!longContext : false;
  const fallbackEffort = submenuAgentStatus?.default_effort || "";
  const displayedEffort = submenuIsCodex
    ? effort || fallbackEffort || "Auto"
    : effort || fallbackEffort || "Auto";
  const fallbackFastService = submenuAgentStatus?.default_fast_service || "";
  const fastModeEnabled =
    (submenuAgentStatus?.name === agent ? fastService : fallbackFastService) ===
    "on";
  const buttonTitle = useMemo(() => {
    if (warnUnavailable) {
      return t("agent.currentUnavailable", { name: agent });
    }
    if (agent && model) {
      return `${agent} · ${model}`;
    }
    return undefined;
  }, [agent, model, t, warnUnavailable]);

  useEffect(() => {
    onOpenChange?.(isOpen);
  }, [isOpen, onOpenChange]);

  useEffect(() => {
    if (!isOpen) return;
    const recompute = () => setPositionTick((t) => t + 1);
    window.visualViewport?.addEventListener("resize", recompute);
    window.addEventListener("resize", recompute);
    return () => {
      window.visualViewport?.removeEventListener("resize", recompute);
      window.removeEventListener("resize", recompute);
    };
  }, [isOpen]);

  useEffect(() => {
    const handlePointerOutside = (e: PointerEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(e.target as Node) &&
        !menuRef.current?.contains(e.target as Node)
      ) {
        setIsOpen(false);
        setSubmenuAgent(null);
        setErrorAgent(null);
        setModelSectionExpanded(true);
        setModeSectionExpanded(false);
        setEffortSectionExpanded(false);
        setServiceTierSectionExpanded(false);
        setMenuBodyHeight(null);
      }
    };
    if (isOpen) {
      document.addEventListener("pointerdown", handlePointerOutside);
      return () =>
        document.removeEventListener("pointerdown", handlePointerOutside);
    }
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen || submenuAgent) {
      return;
    }
    const node = agentColumnRef.current;
    if (!node) {
      return;
    }
    setMenuBodyHeight(
      Math.min(
        AGENT_MENU_MAX_BODY_HEIGHT,
        Math.max(node.scrollHeight, AGENT_MENU_MIN_BODY_HEIGHT),
      ),
    );
  }, [isOpen, submenuAgent, agents.length]);

  useLayoutEffect(() => {
    if (!isOpen || !menuRef.current) {
      return;
    }

    if (viewportMenu) {
      const anchor = dropdownRef.current?.getBoundingClientRect();
      const menu = menuRef.current.getBoundingClientRect();
      if (!anchor) return;
      const viewport = window.visualViewport;
      const viewportLeft = viewport?.offsetLeft ?? 0;
      const viewportTop = viewport?.offsetTop ?? 0;
      const viewportWidth = viewport?.width ?? window.innerWidth;
      const viewportHeight = viewport?.height ?? window.innerHeight;
      const margin = 8;
      const maxLeft = viewportLeft + viewportWidth - menu.width - margin;
      // Match the original absolute positioning (`right: 0`): the menu's
      // right edge stays aligned with the selector's right edge. The portal
      // changes only the stacking context, not the visible placement.
      const left = Math.max(
        viewportLeft + margin,
        Math.min(anchor.right - menu.width, maxLeft),
      );
      const below = anchor.bottom + 8;
      const above = anchor.top - menu.height - 8;
      const top = menuPlacement === "bottom" && below + menu.height <= viewportTop + viewportHeight - margin
        ? below
        : Math.max(viewportTop + margin, above);
      setViewportMenuPosition((current) =>
        current && Math.abs(current.top - top) < 0.5 && Math.abs(current.left - left) < 0.5
          ? current
          : { top, left },
      );
      return;
    }

    const viewport = window.visualViewport;
    const viewportLeft = viewport?.offsetLeft ?? 0;
    const viewportRight = viewportLeft + (viewport?.width ?? window.innerWidth);
    const margin = 8;
    const rect = menuRef.current.getBoundingClientRect();
    let correction = 0;

    if (rect.left < viewportLeft + margin) {
      correction = viewportLeft + margin - rect.left;
    }
    if (rect.right + correction > viewportRight - margin) {
      correction -= rect.right + correction - (viewportRight - margin);
    }

    if (Math.abs(correction) > 0.5) {
      setMenuHorizontalOffset((current) => current + correction);
    }
  }, [
    errorAgent,
    isOpen,
    menuBodyHeight,
    menuHorizontalOffset,
    menuPlacement,
    submenuAgent,
    viewportMenu,
    positionTick,
  ]);

  const handleAgentSelect = useCallback(
    (newAgent: string, nextModel?: string) => {
      onAgentChange(newAgent, nextModel);
      setIsOpen(false);
      setSubmenuAgent(null);
      setErrorAgent(null);
      setModelSectionExpanded(true);
      setModeSectionExpanded(false);
      setEffortSectionExpanded(false);
      setServiceTierSectionExpanded(false);
    },
    [onAgentChange],
  );

  const handleAgentRowClick = useCallback(
    (entry: AgentStatus) => {
      handleAgentSelect(
        entry.name,
        allowDefaultModel
          ? ""
          : entry.default_model_id || entry.current_model_id || "",
      );
    },
    [allowDefaultModel, handleAgentSelect],
  );

  const handleSubmenuToggle = useCallback((entry: AgentStatus) => {
    const isClaude = String(entry.name || "").trim().toLowerCase() === "claude";
    if (
      (entry.models?.length ?? 0) === 0 &&
      (entry.modes?.length ?? 0) === 0 &&
      (entry.efforts?.length ?? 0) === 0 &&
      !entry.supports_fast_service &&
      !isClaude
    ) {
      return;
    }
    setErrorAgent(null);
    setModelSectionExpanded(true);
    setModeSectionExpanded(false);
    setEffortSectionExpanded(false);
    setServiceTierSectionExpanded(false);
    const node = agentColumnRef.current;
    if (node) {
      setMenuBodyHeight(
        Math.min(
          AGENT_MENU_MAX_BODY_HEIGHT,
          Math.max(node.scrollHeight, AGENT_MENU_MIN_BODY_HEIGHT),
        ),
      );
    }
    setSubmenuAgent((prev) => (prev === entry.name ? null : entry.name));
  }, []);

  const handleEffortSelect = useCallback(
    (nextEffort: string) => {
      onEffortChange?.(nextEffort);
      setIsOpen(false);
      setSubmenuAgent(null);
      setErrorAgent(null);
      setModelSectionExpanded(true);
      setModeSectionExpanded(false);
      setEffortSectionExpanded(false);
      setServiceTierSectionExpanded(false);
      setMenuBodyHeight(null);
    },
    [onEffortChange],
  );

  const handleServiceTierSelect = useCallback(
    (nextFastService: "" | "on" | "off") => {
      onFastServiceChange?.(nextFastService);
      setIsOpen(false);
      setSubmenuAgent(null);
      setErrorAgent(null);
      setModelSectionExpanded(true);
      setModeSectionExpanded(false);
      setEffortSectionExpanded(false);
      setServiceTierSectionExpanded(false);
      setMenuBodyHeight(null);
    },
    [onFastServiceChange],
  );

  const handleModeSelect = useCallback(
    (nextMode: string) => {
      onModeChange?.(nextMode);
      setIsOpen(false);
      setSubmenuAgent(null);
      setErrorAgent(null);
      setModelSectionExpanded(true);
      setModeSectionExpanded(false);
      setEffortSectionExpanded(false);
      setServiceTierSectionExpanded(false);
      setMenuBodyHeight(null);
    },
    [onModeChange],
  );

  const handleLongContextToggle = useCallback(() => {
    onLongContextChange?.(!longContextEnabled);
  }, [onLongContextChange, longContextEnabled]);

  const handleAgentRestart = useCallback(
    async (targetAgent: string) => {
      if (!onAgentRestart || restartingAgent) {
        return;
      }
      setRestartingAgent(targetAgent);
      try {
        await onAgentRestart(targetAgent);
      } finally {
        setRestartingAgent((current) =>
          current === targetAgent ? null : current,
        );
      }
    },
    [onAgentRestart, restartingAgent],
  );

  return (
    <div ref={dropdownRef} data-onboarding={onboardingId} style={{ position: "relative" }}>
      <style>{`
        @keyframes agent-refresh-spin {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
      `}</style>
      <button
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          setViewportMenuPosition(null);
          setIsOpen((prev) => {
            const next = !prev;
            if (next) {
              setMenuHorizontalOffset(0);
              const selectedAgent = agents.find((item) => item.name === agent);
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
        title={buttonTitle}
        style={{
          display: "flex",
          alignItems: "center",
          gap: "4px",
          padding: compact ? "4px 4px" : "6px 8px",
          borderRadius: "12px",
          border: "none",
          background: "transparent",
          cursor: "pointer",
          fontSize: "16px",
          transition: "background 0.2s",
          outline: "none",
          position: "relative",
        }}
        onMouseEnter={(e) => {
          if (compact) e.currentTarget.style.background = "rgba(0,0,0,0.05)";
        }}
        onMouseLeave={(e) => {
          if (compact) e.currentTarget.style.background = "transparent";
        }}
      >
        <AgentIcon
          agentName={agent}
          style={{ width: "16px", height: "16px" }}
        />
        {showChevron ? (
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            style={{ color: "var(--text-secondary)" }}
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        ) : null}
        {warnUnavailable && (
          <span
            style={{
              position: "absolute",
              top: "3px",
              right: "3px",
              minWidth: "11px",
              height: "11px",
              padding: "0 2px",
              borderRadius: "50%",
              background: "#d97706",
              color: "#fff",
              fontSize: "9px",
              lineHeight: "11px",
              fontWeight: 700,
              textAlign: "center",
              boxShadow: "0 0 0 1px rgba(255,255,255,0.95)",
            }}
          >
            !
          </span>
        )}
      </button>

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
            agents={agents} agent={agent} agentColumnRef={agentColumnRef} submenuAgent={submenuAgent}
            submenuAgentStatus={submenuAgentStatus} errorAgent={errorAgent}
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
                onAgentRestart={onAgentRestart} restartingAgent={restartingAgent} t={t}
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

