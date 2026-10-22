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
  /** 胶囊里的重置提示：5 小时窗是「HH:MM」，周限是「Ad Bh」。胶囊里不写「重置」二字。 */
  resetText: string;
};

function remainingPercent(window?: CodexRateLimitWindow): number | null {
  const used = window?.used_percent;
  if (typeof used !== "number" || !Number.isFinite(used)) return null;
  return Math.max(0, Math.min(100, Math.round(100 - used)));
}

function formatClockTime(unixSeconds: number): string {
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(new Date(unixSeconds * 1000));
}

/** 距离重置还剩多少天多少小时。 */
function formatRemaining(resetsAt: number, nowMs: number): { days: number; hours: number } {
  const ms = Math.max(0, resetsAt * 1000 - nowMs);
  return {
    days: Math.floor(ms / 86_400_000),
    hours: Math.floor((ms % 86_400_000) / 3_600_000),
  };
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
  // 周限的「Ad Bh」要跟着时间走，每分钟刷新一次。
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
    const timer = window.setInterval(() => setNowMs(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const limits = useMemo<CodexLimitView[]>(() => {
    const views: CodexLimitView[] = [];
    const push = (id: CodexLimitView["id"], label: string, window?: CodexRateLimitWindow) => {
      if (!window) return;
      let resetText = "";
      if (window.resets_at) {
        if (id === "five_hour") {
          resetText = t("codexLimit.resetAtTime", { time: formatClockTime(window.resets_at) });
        } else {
          const { days, hours } = formatRemaining(window.resets_at, nowMs);
          resetText = t("codexLimit.resetInRemaining", { days, hours });
        }
      }
      views.push({
        id,
        label,
        remaining: remainingPercent(window),
        resetText,
      });
    };
    push("five_hour", t("codexLimit.fiveHour"), status?.five_hour);
    push("weekly", t("codexLimit.weekly"), status?.weekly);
    return views;
  }, [status, t, nowMs]);

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
