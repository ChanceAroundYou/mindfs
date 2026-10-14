import { useState, useRef, useEffect, useLayoutEffect, useCallback, useMemo } from "react";
import type { AgentStatus } from "../../services/agents";
import { filterAgentModels } from "../../shared/modelFiltering";
import { useI18n } from "../../i18n/index";
import {
  AGENT_MENU_MAX_BODY_HEIGHT,
  AGENT_MENU_MIN_BODY_HEIGHT,
  AGENT_MODEL_SEARCH_THRESHOLD,
} from "./AgentSelectorParts";
import { claudeModelBase, isClaudeAliasModelName, strip1MSuffix } from "./agentSelectorUtils";

export type AgentSelectorProps = {
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

export function useAgentSelector({
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
  compact: _compact,
  warnUnavailable = false,
  menuPlacement = "top",
  showChevron: _showChevron,
  defaultExpandOptions = false,
  onboardingId: _onboardingId,
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

  return {
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
    t: t as (key: any, fallback?: any) => string,
  };
}
