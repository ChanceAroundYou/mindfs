// 控制面同步状态的轮询与显式切换。
//
// 视图层（components）按仓库规则不能直接 import services，这一层就是那条边界：
// 组件只拿「当前状态 + 要不要显示 + 能做什么」，不碰请求细节。
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  conflictReminder,
  controlModeBanner,
  controlSyncActionFor,
  CONTROL_CONFLICTS_EVENT,
  type ControlBannerAction,
  type ControlSyncStatus,
} from "../shared/controlMode";
import { fetchControlSyncStatus, switchControlSyncMode } from "../services/net/controlSync";

// 与服务端探测间隔同量级：探测 10 秒一拍、3 次失败才降级，前端更密也看不出差别。
const POLL_MS = 10_000;

export function useControlSyncStatus() {
  const [status, setStatus] = useState<ControlSyncStatus | null>(null);
  // 端点 404 = 本机没有这个能力（主节点 / 未启用）。关掉后不再轮询。
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchControlSyncStatus();
        if (cancelled) return;
        if (!next) {
          setEnabled(false);
          setStatus(null);
          return;
        }
        setStatus(next);
      } catch {
        // 网络抖动或 bootstrap 尚未就绪：保留上一份状态，下一拍再试。
        // 只有明确的 404（在 service 里已折成 null）才关闭轮询。
      }
    };
    void load();
    const timer = window.setInterval(() => {
      void load();
    }, POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [enabled]);

  const banner = useMemo(
    () =>
      controlModeBanner(status?.mode, {
        conflictCount: status?.conflict_count ?? 0,
        blockedCode: status?.blocked_code,
      }),
    [status?.mode, status?.conflict_count, status?.blocked_code],
  );

  // 一次性冲突提醒按事件 id 去重：关掉之后同一次进入不再弹回来（见 conflictReminder）。
  const [dismissedConflicts, setDismissedConflicts] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const reminder = useMemo(
    () => conflictReminder(status, dismissedConflicts),
    [status, dismissedConflicts],
  );
  const dismissConflictReminder = useCallback(() => {
    if (!reminder) return;
    setDismissedConflicts((prev) => new Set(prev).add(reminder.id));
  }, [reminder]);

  const runAction = useCallback(
    async (action: ControlBannerAction) => {
      if (action === "view-conflicts") {
        // 冲突面板（T17）订阅这个事件；面板就绪前点它不会有副作用。
        window.dispatchEvent(new CustomEvent(CONTROL_CONFLICTS_EVENT));
        // 「去处理了」等价于关掉提醒；真的没处理完，常驻横幅还在。
        dismissConflictReminder();
        return;
      }
      if (busy) return;
      setBusy(true);
      try {
        const next = await switchControlSyncMode(controlSyncActionFor(action));
        if (next) setStatus(next);
      } catch {
        // 切换失败时状态由下一拍轮询纠正，不额外弹错。
      } finally {
        setBusy(false);
      }
    },
    [busy, dismissConflictReminder],
  );

  return { status, enabled, busy, banner, reminder, runAction, dismissConflictReminder };
}