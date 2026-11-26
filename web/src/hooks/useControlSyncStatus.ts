// 控制面同步状态的轮询与显式切换。
//
// 视图层（components）按仓库规则不能直接 import services，这一层就是那条边界：
// 组件只拿「当前状态 + 要不要显示 + 能做什么」，不碰请求细节。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  conflictReminder,
  controlModeBanner,
  controlSyncActionFor,
  controlTransitionNotice,
  CONTROL_CONFLICTS_EVENT,
  type ControlBannerAction,
  type ControlMode,
  type ControlSyncStatus,
  type ControlTransitionNotice,
} from "../shared/controlMode";
import type { PairingDraft } from "../shared/controlPairing";
import {
  fetchControlSyncPairing,
  fetchControlSyncStatus,
  pairControlSync,
  switchControlSyncMode,
  type ControlSyncPairResult,
  type ControlSyncPairingState,
  type PairControlSyncOutcome,
} from "../services/net/controlSync";

export type { ControlSyncPairResult, ControlSyncPairingState, PairControlSyncOutcome };

// 与服务端探测间隔同量级：探测 10 秒一拍、3 次失败才降级，前端更密也看不出差别。
const POLL_MS = 10_000;

export function useControlSyncStatus() {
  const [status, setStatus] = useState<ControlSyncStatus | null>(null);
  // 端点 404 = 本机没有这个能力（主节点 / 未启用）。关掉后不再轮询。
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  // 未配对的边缘：只注册了 /pair 与它的只读探针，/status 同样 404。
  // 这两件事在前端必须分开，否则配对表单永远不会出现。
  const [pairing, setPairing] = useState<ControlSyncPairingState | null>(null);
  const [pairingBusy, setPairingBusy] = useState(false);
  const [pairingOpen, setPairingOpen] = useState(false);
  const [paired, setPaired] = useState<ControlSyncPairResult | null>(null);
  const [pairingFailure, setPairingFailure] = useState<PairControlSyncOutcome | null>(null);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchControlSyncStatus();
        if (cancelled) return;
        if (!next) {
          // /status 404 有两种可能：这台机器根本不同步，或者它是等着配对的边缘。
          // 后者的页面上必须出现配对表单 —— 那是拿到 token 的唯一入口。
          setEnabled(false);
          setStatus(null);
          setPairing(await fetchControlSyncPairing());
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

  // 主控离线 / 恢复的一次性提示：只在模式迁移时出现；首拍只记基线，
  // 刷新页面不把「当前已离线」当成一次新切换。点掉后按事件 id 去重。
  const [notice, setNotice] = useState<ControlTransitionNotice | null>(null);
  const prevModeRef = useRef<ControlMode | null>(null);
  const dismissedTransitionsRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!status) return;
    const prev = prevModeRef.current;
    prevModeRef.current = status.mode;
    if (!prev) return;
    const next = controlTransitionNotice(prev, status);
    if (!next || dismissedTransitionsRef.current.has(next.id)) return;
    setNotice((cur) => (cur?.id === next.id ? cur : next));
  }, [status]);
  const dismissNotice = useCallback(() => {
    setNotice((cur) => {
      if (cur) dismissedTransitionsRef.current.add(cur.id);
      return null;
    });
  }, []);

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

  const submitPairing = useCallback(
    async (draft: PairingDraft) => {
      if (pairingBusy) return;
      setPairingBusy(true);
      setPairingFailure(null);
      try {
        const outcome = await pairControlSync(draft);
        if (outcome.ok) {
          setPaired(outcome.result);
          // 配好之后这台机器仍是「未配对」进程：路由在启动期装配，要重启才生效。
          // 表单留在原处并改成「已连接，重启后生效」，不能默默消失 ——
          // 用户会以为没点中。
          return;
        }
        setPairingFailure(outcome);
      } finally {
        setPairingBusy(false);
      }
    },
    [pairingBusy],
  );

  return {
    status,
    enabled,
    busy,
    banner,
    reminder,
    runAction,
    dismissConflictReminder,
    notice,
    dismissNotice,
    pairing,
    pairingOpen,
    openPairing: useCallback(() => setPairingOpen(true), []),
    closePairing: useCallback(() => setPairingOpen(false), []),
    pairingBusy,
    pairingFailure,
    paired,
    submitPairing,
  };
}