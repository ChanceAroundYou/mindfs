// 会话维护的状态与副作用。视图只调用这里，不碰 services（仓库三层边界）。
import { useCallback, useEffect, useMemo, useState } from "react";
import * as api from "../services/session/sessionMaintenanceApi";
import {
  SESSION_HEALTH_EVENT,
  SESSION_MAINTENANCE_EVENT,
  type MaintenanceCleanResult,
  type MaintenanceLockResult,
  type MaintenanceReport,
  type RuntimeIssue,
  type RuntimeSweepReport,
  type SessionMaintenanceTarget,
} from "../shared/sessionMaintenance";

export type SessionMaintenanceMode = "session" | "health";

export type SessionMaintenanceOptions = {
  mode?: SessionMaintenanceMode;
  /** 会话体检要逐项目查运行时问题；诊断本身 rootless，跨项目会话来自它。 */
  rootIds?: string[];
};

type BusyKind = "diagnose" | "locks" | "images" | "purge" | "sweep" | null;

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}

export function useSessionMaintenance(options: SessionMaintenanceOptions = {}) {
  const mode = options.mode ?? "session";
  const rootIds = options.rootIds ?? [];
  const rootIdsKey = rootIds.join("\0");

  const [target, setTarget] = useState<SessionMaintenanceTarget | null>(null);
  const [healthOpen, setHealthOpen] = useState(false);
  const [report, setReport] = useState<MaintenanceReport | null>(null);
  const [issues, setIssues] = useState<RuntimeIssue[]>([]);
  const [sweep, setSweep] = useState<RuntimeSweepReport | null>(null);
  const [lastClean, setLastClean] = useState<MaintenanceCleanResult[]>([]);
  const [lockResult, setLockResult] = useState<MaintenanceLockResult | null>(null);
  const [busy, setBusy] = useState<BusyKind>(null);
  const [error, setError] = useState<string | null>(null);

  // 两个入口都以事件通知：会话标题栏 ⋯ 带会话身份，侧栏 ⋯ 只开体检面板。
  useEffect(() => {
    if (mode === "session") {
      const onOpen = (event: Event) => {
        const detail = (event as CustomEvent<SessionMaintenanceTarget>).detail;
        if (!detail?.sessionKey) return;
        setTarget(detail);
        setReport(null);
        setLastClean([]);
        setError(null);
      };
      window.addEventListener(SESSION_MAINTENANCE_EVENT, onOpen);
      return () => window.removeEventListener(SESSION_MAINTENANCE_EVENT, onOpen);
    }
    const onHealth = () => {
      setHealthOpen(true);
      setReport(null);
      setIssues([]);
      setError(null);
    };
    window.addEventListener(SESSION_HEALTH_EVENT, onHealth);
    return () => window.removeEventListener(SESSION_HEALTH_EVENT, onHealth);
  }, [mode]);

  const reload = useCallback(async () => {
    if (mode === "session" && !target) return;
    setBusy("diagnose");
    setError(null);
    try {
      const [diagnose, issueLists] = await Promise.all([
        api.fetchMaintenanceDiagnose(mode === "session" ? target?.rootId : undefined, target?.nodeId),
        mode === "health"
          ? Promise.allSettled(rootIdsKey ? rootIdsKey.split("\0").map((id) => api.fetchRuntimeIssues(id)) : [])
          : Promise.resolve([]),
      ]);
      setReport(diagnose);
      setIssues(
        issueLists.flatMap((entry) =>
          entry.status === "fulfilled" ? (entry.value.issues ?? []) : [],
        ) as RuntimeIssue[],
      );
      setSweep(null);
      if (mode === "health" && issueLists.length > 0 && issueLists.every((entry) => entry.status === "rejected")) {
        setError(errorText((issueLists[0] as PromiseRejectedResult).reason));
      }
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setBusy(null);
    }
  }, [mode, target?.rootId, target?.nodeId, rootIdsKey]);

  useEffect(() => {
    if (mode === "session" && !target) return;
    if (mode === "health" && !healthOpen) return;
    void reload();
  }, [mode, target?.sessionKey, healthOpen, reload]);

  const close = useCallback(() => {
    setTarget(null);
    setHealthOpen(false);
    setReport(null);
    setIssues([]);
    setLastClean([]);
    setLockResult(null);
    setSweep(null);
    setError(null);
  }, []);

  const cleanLocks = useCallback(async () => {
    setBusy("locks");
    setError(null);
    try {
      const result = await api.cleanMaintenanceLocks(target?.nodeId);
      setLockResult(result);
      await reload();
      return result;
    } catch (cause) {
      setError(errorText(cause));
      return null;
    } finally {
      setBusy(null);
    }
  }, [target?.nodeId, reload]);

  const cleanImages = useCallback(
    async (sessionKey: string, rootId?: string) => {
      setBusy("images");
      setError(null);
      try {
        const results = await api.cleanSessionImages(sessionKey, rootId, target?.nodeId);
        setLastClean(results);
        await reload();
        return results;
      } catch (cause) {
        setError(errorText(cause));
        return [];
      } finally {
        setBusy(null);
      }
    },
    [target?.nodeId, reload],
  );

  const purgeEmpty = useCallback(
    async (sessionKey: string, rootId?: string) => {
      setBusy("purge");
      setError(null);
      try {
        const results = await api.purgeEmptySession(sessionKey, rootId, target?.nodeId);
        setLastClean(results);
        await reload();
        return results;
      } catch (cause) {
        setError(errorText(cause));
        return [];
      } finally {
        setBusy(null);
      }
    },
    [target?.nodeId, reload],
  );

  const sweepRuntime = useCallback(async () => {
    setBusy("sweep");
    setError(null);
    const roots = rootIdsKey ? rootIdsKey.split("\0") : [];
    try {
      const results = await Promise.allSettled(roots.map((id) => api.sweepRuntimeIssues(id)));
      const done = results.flatMap((entry) => (entry.status === "fulfilled" ? [entry.value] : []));
      const combined: RuntimeSweepReport = {
        scanned: done.reduce((sum, value) => sum + (value.scanned ?? 0), 0),
        repaired: done.reduce((sum, value) => sum + (value.repaired ?? 0), 0),
        skipped: done.reduce((sum, value) => sum + (value.skipped ?? 0), 0),
        failed: done.reduce((sum, value) => sum + (value.failed ?? 0), 0),
        details: done.flatMap((value) => value.details ?? []),
      };
      setSweep(combined);
      await reload();
      if (results.some((entry) => entry.status === "rejected")) {
        setError(errorText((results.find((entry) => entry.status === "rejected") as PromiseRejectedResult).reason));
      }
      return combined;
    } catch (cause) {
      setError(errorText(cause));
      return null;
    } finally {
      setBusy(null);
    }
  }, [rootIdsKey, reload]);

  const canRepoint = useMemo(() => api.canRepointSession(target), [target]);

  return {
    mode,
    open: mode === "session" ? !!target : healthOpen,
    target,
    report,
    issues,
    sweep,
    lastClean,
    lockResult,
    busy,
    error,
    canRepoint,
    close,
    reload,
    cleanLocks,
    cleanImages,
    purgeEmpty,
    sweepRuntime,
  };
}

export type SessionMaintenanceController = ReturnType<typeof useSessionMaintenance>;
