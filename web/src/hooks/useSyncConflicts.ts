// 冲突面板的打开、读取与裁决。
//
// 视图层（components）不能直接 import services，这一层是那条边界：组件只拿
// 「开没开 / 有哪些卡 / 能否操作」，请求与刷新都在这里。
import { useCallback, useEffect, useState } from "react";
import { CONTROL_CONFLICTS_EVENT } from "../shared/controlMode";
import type { SyncConflictChoice, SyncConflictView } from "../shared/syncConflicts";
import {
  fetchSyncConflicts,
  resolveConflictPayloadEdits,
  resolveSyncConflict,
  resolveSyncConflicts,
  type SyncConflictPayloadEdit,
} from "../services/net/controlSync";

export type SyncConflictError = "load" | "resolve" | null;

export function useSyncConflicts() {
  const [open, setOpen] = useState(false);
  // null = 端点不存在（主节点 / 未启用），面板不出现；[] = 读到了，但没有冲突。
  const [conflicts, setConflicts] = useState<SyncConflictView[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<SyncConflictError>(null);

  const reload = useCallback(async () => {
    try {
      const next = await fetchSyncConflicts();
      if (next === null) {
        setConflicts(null);
        setOpen(false);
        return;
      }
      setConflicts(next);
      setError(null);
    } catch {
      // 读失败不清空已有列表：清空等于告诉用户「没有冲突了」，比报错更危险。
      setError("load");
    }
  }, []);

  useEffect(() => {
    const onOpen = () => {
      setOpen(true);
      void reload();
    };
    window.addEventListener(CONTROL_CONFLICTS_EVENT, onOpen);
    return () => window.removeEventListener(CONTROL_CONFLICTS_EVENT, onOpen);
  }, [reload]);

  const resolveOne = useCallback(
    async (id: string, choice: SyncConflictChoice, payload?: unknown): Promise<boolean> => {
      setBusy(true);
      setError(null);
      try {
        await resolveSyncConflict(id, choice, payload);
        await reload();
        return true;
      } catch {
        setError("resolve");
        return false;
      } finally {
        setBusy(false);
      }
    },
    [reload],
  );

  // 批量：一次确认触发一次提交，串行发完再刷新（逐条请求留在 service 里）。
  const resolveMany = useCallback(
    async (
      views: readonly SyncConflictView[],
      choice: Exclude<SyncConflictChoice, "payload">,
    ): Promise<void> => {
      setBusy(true);
      setError(null);
      try {
        const result = await resolveSyncConflicts(views, choice);
        await reload();
        // 刷新会清掉 error，所以失败提示要在刷新之后再写，否则部分失败被静默吞掉。
        if (result.failed > 0) setError("resolve");
      } catch {
        setError("resolve");
      } finally {
        setBusy(false);
      }
    },
    [reload],
  );

  // 批量自定义合并：每条卡各自带 payload，沿用同一套串行与 404/409 计数口径。
  const resolveManyPayloads = useCallback(
    async (edits: readonly SyncConflictPayloadEdit[]): Promise<boolean> => {
      setBusy(true);
      setError(null);
      try {
        const result = await resolveConflictPayloadEdits(edits);
        await reload();
        if (result.failed > 0) setError("resolve");
        return result.failed === 0;
      } catch {
        setError("resolve");
        return false;
      } finally {
        setBusy(false);
      }
    },
    [reload],
  );

  const close = useCallback(() => {
    setOpen(false);
    setError(null);
  }, []);

  return {
    open,
    close,
    conflicts,
    busy,
    error,
    reload,
    resolveOne,
    resolveMany,
    resolveManyPayloads,
  };
}