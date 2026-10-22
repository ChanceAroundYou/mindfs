import React, { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { protectedAPIReady } from "../../services/net/api";
import { bootstrapService } from "../../services/net/bootstrap";
import {
  fetchAgentMemory,
  releaseAgentSessions,
  type AgentMemorySnapshot,
} from "../../services/agentMemory";

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "—";
  const gib = value / (1024 ** 3);
  if (gib >= 1) return `${gib.toFixed(gib >= 10 ? 0 : 1)} GB`;
  const mib = value / (1024 ** 2);
  if (mib >= 1) return `${Math.round(mib)} MB`;
  return `${Math.round(value / 1024)} KB`;
}

export type AgentMemoryIndicatorProps = {
  refreshToken?: number;
  /** 内存统计与释放都是节点本地行为：必须显式带上项目所在节点，否则会静默打当前激活节点。 */
  nodeId?: string;
};

export function useAgentMemory({ refreshToken = 0, nodeId }: AgentMemoryIndicatorProps) {
  const { t } = useI18n();
  const [snapshot, setSnapshot] = useState<AgentMemorySnapshot | null>(null);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [releasing, setReleasing] = useState(false);
  const [error, setError] = useState("");
  const [releaseResult, setReleaseResult] = useState<{ released: number; failed: number } | null>(null);
  const [apiReady, setAPIReady] = useState(protectedAPIReady);
  const requestRef = useRef(0);

  const refresh = async () => {
    const request = ++requestRef.current;
    setLoading(true);
    setError("");
    try {
      const next = await fetchAgentMemory(nodeId);
      if (request === requestRef.current) {
        setSnapshot(next);
      }
    } catch (err) {
      if (request === requestRef.current) {
        setError(String((err as Error)?.message || t("agentMemory.loadFailed")));
      }
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  };

  useEffect(() => {
    return bootstrapService.subscribe(() => {
      setAPIReady(protectedAPIReady());
    });
  }, []);

  useEffect(() => {
    if (!apiReady) return;
    void refresh();
    return () => {
      requestRef.current += 1;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady, refreshToken, nodeId]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !releasing) setOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, releasing]);

  const agents = useMemo(
    () => (snapshot?.agents || []).filter((item) => item.process_count > 0 || item.session_count > 0),
    [snapshot?.agents],
  );
  const sessionCount = agents.reduce((sum, item) => sum + Math.max(0, item.session_count || 0), 0);

  const handleOpen = () => {
    setOpen(true);
    setReleaseResult(null);
    void refresh();
  };

  const handleRelease = async () => {
    if (releasing || sessionCount <= 0) return;
    setReleasing(true);
    setError("");
    setReleaseResult(null);
    try {
      const result = await releaseAgentSessions(nodeId);
      requestRef.current += 1;
      setSnapshot(result);
      setReleaseResult({ released: result.released_sessions, failed: result.failed_sessions });
    } catch (err) {
      setError(String((err as Error)?.message || t("agentMemory.releaseFailed")));
    } finally {
      setReleasing(false);
    }
  };

  return {
    snapshot,
    open,
    loading,
    releasing,
    error,
    releaseResult,
    agents,
    sessionCount,
    setOpen,
    handleOpen,
    handleRelease,
  };
}
