import React, { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import {
  consumeCodexRateLimitReset,
  fetchCodexRateLimits,
  type CodexRateLimitStatus,
  type CodexRateLimitWindow,
} from "../../services/codexRateLimits";

const FOCUS_REFRESH_MAX_AGE_MS = 30 * 60 * 1000;

export type CodexRateLimitIndicatorProps = {
  agent: string;
  refreshToken?: number;
  nodeId?: string;
  onStatusChange?: (status: CodexRateLimitStatus | null) => void;
};

/** 一个限额窗口的展示视图：剩余百分比与重置时间合并成一条。 */
export type CodexLimitView = {
  id: "five_hour" | "weekly";
  label: string;
  remaining: number | null;
  resetTime: string;
  resetCountdown: string;
};

function remainingPercent(window?: CodexRateLimitWindow): number | null {
  const used = window?.used_percent;
  if (typeof used !== "number" || !Number.isFinite(used)) return null;
  return Math.max(0, Math.min(100, Math.round(100 - used)));
}

function formatResetTime(unixSeconds: number | undefined): string {
  if (!unixSeconds) return "";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(unixSeconds * 1000));
}

function formatResetCountdown(unixSeconds: number | undefined, nowMs: number): string {
  if (!unixSeconds) return "";
  const remainingMinutes = Math.max(0, Math.ceil((unixSeconds * 1000 - nowMs) / 60_000));
  const days = Math.floor(remainingMinutes / (24 * 60));
  const hours = Math.floor((remainingMinutes % (24 * 60)) / 60);
  const minutes = remainingMinutes % 60;
  if (days > 0) return `${days}d·${hours}h`;
  if (hours > 0) return `${hours}h·${minutes}m`;
  return `${minutes}m`;
}

export function useCodexRateLimit({
  agent,
  refreshToken = 0,
  nodeId,
  onStatusChange,
}: CodexRateLimitIndicatorProps) {
  const { t } = useI18n();
  const [status, setStatus] = useState<CodexRateLimitStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [resetOutcome, setResetOutcome] = useState("");
  const [error, setError] = useState("");
  const [nowMs, setNowMs] = useState(() => Date.now());
  const requestRef = useRef(0);
  const activeRequestsRef = useRef(0);
  const lastSuccessfulFetchAtRef = useRef(0);
  const idempotencyKeyRef = useRef("");

  const refresh = async () => {
    if (agent !== "codex") return;
    const request = ++requestRef.current;
    activeRequestsRef.current += 1;
    setLoading(true);
    setError("");
    try {
      const next = await fetchCodexRateLimits(agent, nodeId);
      if (request !== requestRef.current) return;
      lastSuccessfulFetchAtRef.current = Date.now();
      setStatus(next);
      onStatusChange?.(next);
    } catch (err) {
      if (request !== requestRef.current) return;
      setError(String((err as Error)?.message || t("codexLimit.loadFailed")));
      setStatus(null);
      onStatusChange?.(null);
    } finally {
      activeRequestsRef.current = Math.max(0, activeRequestsRef.current - 1);
      if (request === requestRef.current) setLoading(false);
    }
  };

  useEffect(() => {
    if (agent !== "codex") {
      requestRef.current += 1;
      lastSuccessfulFetchAtRef.current = 0;
      setStatus(null);
      setError("");
      setConfirmOpen(false);
      onStatusChange?.(null);
      return;
    }
    void refresh();
    // Keep the message_done-triggered refresh in addition to focus-based staleness checks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent, refreshToken, nodeId]);

  useEffect(() => {
    if (agent !== "codex") return;
    const onFocus = () => {
      const age = Date.now() - lastSuccessfulFetchAtRef.current;
      if (age < FOCUS_REFRESH_MAX_AGE_MS || activeRequestsRef.current > 0) return;
      void refresh();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
    // The listener is rebound when the selected agent changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent]);

  useEffect(() => {
    if (!confirmOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !resetting) setConfirmOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [confirmOpen, resetting]);

  useEffect(() => {
    if (!status?.weekly?.resets_at && !status?.five_hour?.resets_at) return;
    setNowMs(Date.now());
    const timer = window.setInterval(() => setNowMs(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, [status?.weekly?.resets_at, status?.five_hour?.resets_at]);

  const limits = useMemo<CodexLimitView[]>(() => {
    const views: CodexLimitView[] = [];
    const push = (id: CodexLimitView["id"], label: string, window?: CodexRateLimitWindow) => {
      if (!window) return;
      views.push({
        id,
        label,
        remaining: remainingPercent(window),
        resetTime: formatResetTime(window.resets_at),
        resetCountdown: formatResetCountdown(window.resets_at, nowMs),
      });
    };
    push("five_hour", t("codexLimit.fiveHour"), status?.five_hour);
    push("weekly", t("codexLimit.weekly"), status?.weekly);
    return views;
  }, [status, nowMs, t]);

  const resetCount = Math.max(0, status?.reset_credits?.available_count ?? 0);
  const canReset = resetCount > 0 && !loading;
  const firstCredit = useMemo(
    () => status?.reset_credits?.credits?.find((credit) => credit.status === "available") || status?.reset_credits?.credits?.[0],
    [status],
  );

  const handleReset = async () => {
    if (!canReset || resetting) return;
    if (!idempotencyKeyRef.current) idempotencyKeyRef.current = crypto.randomUUID();
    setResetting(true);
    setError("");
    setResetOutcome("");
    try {
      const result = await consumeCodexRateLimitReset(
        idempotencyKeyRef.current,
        firstCredit?.id,
        agent,
        nodeId,
      );
      setStatus(result.status);
      onStatusChange?.(result.status);
      setResetOutcome(result.outcome);
      idempotencyKeyRef.current = "";
      if (result.outcome === "reset" || result.outcome === "alreadyRedeemed") {
        setConfirmOpen(false);
      }
    } catch (err) {
      setError(String((err as Error)?.message || t("codexLimit.resetFailed")));
    } finally {
      setResetting(false);
    }
  };

  return {
    status,
    loading,
    confirmOpen,
    resetting,
    resetOutcome,
    error,
    limits,
    resetCount,
    canReset,
    firstCredit,
    setConfirmOpen,
    setResetOutcome,
    setError,
    handleReset,
  };
}
