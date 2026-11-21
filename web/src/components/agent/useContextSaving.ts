import { useEffect, useRef, useState } from "react";
import { protectedAPIReady } from "../../services/net/api";
import { bootstrapService } from "../../services/net/bootstrap";
import { compactSession, fetchSessionContextSaving, type SessionContextSaving } from "../../services/agent/contextSaving";

export type ContextSavingIndicatorProps = {
  sessionKey: string;
  rootId?: string;
  /** 数据面是节点本地的：必须显式带节点，否则会静默打当前激活节点。 */
  nodeId?: string;
  /**
   * 会话是否还有回合在跑。一次折叠只发生在回合结束时，所以在跑的时候不轮询；
   * 由 true 落到 false 就是「刚结束一轮」，刷新一次即可。
   */
  pending?: boolean;
};

/** 折叠后节省的字节数；无数据或没省返回 0。 */
export function contextSavingBytes(saving: SessionContextSaving | null): number {
  const saved = Number(saving?.latest?.saved_bytes || 0);
  return Number.isFinite(saved) && saved > 0 ? saved : 0;
}

export function useContextSaving({ sessionKey, rootId, nodeId, pending = false }: ContextSavingIndicatorProps) {
  const [saving, setSaving] = useState<SessionContextSaving | null>(null);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [apiReady, setAPIReady] = useState(protectedAPIReady);
  const [compacting, setCompacting] = useState(false);
  const requestRef = useRef(0);
  const wasPendingRef = useRef(pending);
  const compactTimerRef = useRef<number | null>(null);

  const refresh = async () => {
    if (!sessionKey) return;
    const request = ++requestRef.current;
    setLoading(true);
    setError("");
    try {
      const next = await fetchSessionContextSaving(sessionKey, rootId, nodeId);
      if (request === requestRef.current) setSaving(next);
    } catch (err) {
      if (request === requestRef.current) setError(String((err as Error)?.message || ""));
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  };

  useEffect(() => {
    return bootstrapService.subscribe(() => setAPIReady(protectedAPIReady()));
  }, []);

  // 会话切换与首次挂载各拉一次。
  useEffect(() => {
    setSaving(null);
    if (!apiReady) return;
    void refresh();
    return () => {
      requestRef.current += 1;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady, sessionKey, rootId, nodeId]);

  // 卸载或换会话时把「等压缩回合跑完」的补偿刷新取消掉，别让它打旧会话。
  useEffect(() => {
    return () => {
      if (compactTimerRef.current !== null) {
        window.clearTimeout(compactTimerRef.current);
        compactTimerRef.current = null;
      }
    };
  }, [sessionKey]);

  // 回合结束刷新一次；这一轮到底折没折叠只有服务端知道。
  useEffect(() => {
    const wasPending = wasPendingRef.current;
    wasPendingRef.current = pending;
    if (!apiReady || !sessionKey) return;
    if (wasPending && !pending) void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending, apiReady, sessionKey]);

  const savedBytes = contextSavingBytes(saving);
  // 又久又大 / 又大不久：即使一次都没折叠过，胶囊也要出现 —— 它就是这两个
  // 档位的提示载体。normal 与 disabled 不占位置。
  const hygiene = saving?.hygiene ?? null;
  const hygieneBand = hygiene?.state === "notice" || hygiene?.state === "hint";
  const visible = savedBytes > 0 || !!saving?.requests || hygieneBand;

  const compact = async () => {
    if (!sessionKey || compacting) return;
    setCompacting(true);
    setError("");
    try {
      await compactSession(sessionKey, rootId, nodeId);
      // 压缩是一个真实回合，服务端立刻返回 202：先刷一次，再等它跑完补一次。
      void refresh();
      if (compactTimerRef.current !== null) window.clearTimeout(compactTimerRef.current);
      compactTimerRef.current = window.setTimeout(() => {
        compactTimerRef.current = null;
        setCompacting(false);
        void refresh();
      }, 8000);
    } catch (err) {
      setCompacting(false);
      setError(String((err as Error)?.message || ""));
    }
  };

  return {
    saving,
    open,
    loading,
    error,
    savedBytes,
    hygiene,
    visible,
    compacting,
    setOpen,
    handleOpen: () => {
      setOpen(true);
      void refresh();
    },
    compact,
    refresh,
  };
}
