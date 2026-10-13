import { useState } from "react";
import type { SessionItem, SlashCommandResult } from "./appSession";

/** 会话选择、队列刷新和抽屉交互状态。 */
export function useAppSessionSelectionState() {
  const [selectedSession, setSelectedSession] = useState<SessionItem | null>(null);
  const [selectedSessionLoading, setSelectedSessionLoading] = useState(false);
  const [drawerLoadingSessionByRoot, setDrawerLoadingSessionByRoot] = useState<Record<string, string>>({});
  const [activeBoundSessionKey, setActiveBoundSessionKey] = useState<string | null>(null);
  const [pendingPlanMode, setPendingPlanMode] = useState(false);
  const [currentSession, setCurrentSession] = useState<SessionItem | null>(null);
  const [cacheVersion, setCacheVersion] = useState(0);
  const [slashCommandResults, setSlashCommandResults] = useState<Record<string, SlashCommandResult>>({});
  const [copiedSlashCommandKeys, setCopiedSlashCommandKeys] = useState<Record<string, true>>({});
  const [queueVersion, setQueueVersion] = useState(0);
  const [interactionMode, setInteractionMode] = useState<"main" | "drawer">("main");
  const [agentsVersion, setAgentsVersion] = useState(0);
  const [codexRateLimitsRefreshToken, setCodexRateLimitsRefreshToken] = useState(0);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);

  return {
    selectedSession, setSelectedSession, selectedSessionLoading, setSelectedSessionLoading,
    drawerLoadingSessionByRoot, setDrawerLoadingSessionByRoot, activeBoundSessionKey,
    setActiveBoundSessionKey, pendingPlanMode, setPendingPlanMode, currentSession, setCurrentSession,
    cacheVersion, setCacheVersion, slashCommandResults, setSlashCommandResults, copiedSlashCommandKeys,
    setCopiedSlashCommandKeys, queueVersion, setQueueVersion, interactionMode, setInteractionMode,
    agentsVersion, setAgentsVersion, codexRateLimitsRefreshToken, setCodexRateLimitsRefreshToken,
    isDrawerOpen, setIsDrawerOpen,
  };
}
