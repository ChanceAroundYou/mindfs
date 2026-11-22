// 控制面同步的模式词汇与纯映射。
//
// 放在 shared：视图（components）按仓库规则不能直接 import services，而「哪个模式
// 配哪个按钮」既是渲染依据、也是用例钉的对象，所以它必须是一个视图与网络层都能
// 引用的纯模块。真正的请求仍留在 services/net/controlSync.ts。
import type { MessageKey } from "../i18n";

export type ControlMode = "online" | "offline-local" | "reconciling" | "conflict-review";

/** 显式切换只允许两种动作，与 POST /api/control-sync/mode 的取值一一对应。 */
export type ControlSyncAction = "local" | "retry";

/** 冲突面板就绪前，横幅只负责喊人：事件名是两层之间唯一的接口。 */
export const CONTROL_CONFLICTS_EVENT = "mindfs:control-conflicts";

/** 服务端记下的一次模式迁移（`GET /api/control-sync/status` 的 `recent_events`）。 */
export type ControlModeEvent = {
  id: string;
  from: ControlMode;
  to: ControlMode;
  reason?: string;
  at: string;
};

export type ControlSyncStatus = {
  mode: ControlMode;
  base_rev?: number;
  primary_reachable?: boolean;
  pending_count?: number;
  conflict_count?: number;
  blocked_reason?: string;
  last_primary_at?: string;
  last_error?: string;
  recent_events?: ControlModeEvent[];
};

/** 横幅按钮。三种动作合起来覆盖「切本地 / 重试 / 查看冲突」。 */
export type ControlBannerAction = "retry" | "switch-local" | "view-conflicts";

export type ControlModeBannerState = {
  tone: "online" | "local" | "syncing" | "conflict";
  title: MessageKey;
  detail: MessageKey;
  /** 该模式下可用的动作，按显示顺序排列；渲染层只照着画，不自己 if/else。 */
  actions: ControlBannerAction[];
};

const CONTROL_MODES: readonly ControlMode[] = [
  "online",
  "offline-local",
  "reconciling",
  "conflict-review",
];

export function isControlMode(value: unknown): value is ControlMode {
  return typeof value === "string" && (CONTROL_MODES as readonly string[]).includes(value);
}

/**
 * 四态到横幅的纯映射。「没有这个能力」（null）不出横幅；在线也出一条**弱化**横幅。
 *
 * 在线为什么也画：主节点随时可能断，用户得有看得见的入口主动切到本地模式，
 * 而不是等真断了再找过来。它必须弱化（见 tone 与渲染层），否则每条页面顶上
 * 都挂一条「一切正常」的装饰条，反而把真正需要的告警淹了。
 *
 * 抽成纯函数是为了让「哪个状态配哪些按钮」能被用例直接钉住 —— 渲染层只照着画，
 * 不再自己 if/else 一遍，避免两个地方对同一个状态给出不同结论。
 */
export function controlModeBanner(
  mode: ControlMode | null | undefined,
  opts: { conflictCount?: number } = {},
): ControlModeBannerState | null {
  switch (mode) {
    case "online":
      return {
        tone: "online",
        title: "controlMode.online",
        detail: "controlMode.onlineDetail",
        actions: ["switch-local"],
      };
    case "offline-local": {
      // 本地模式下「切本地」是空操作，只给重试；确实攒了冲突才把查看入口带出来。
      const actions: ControlBannerAction[] = ["retry"];
      if ((opts.conflictCount ?? 0) > 0) actions.push("view-conflicts");
      return {
        tone: "local",
        title: "controlMode.local",
        detail: "controlMode.localDetail",
        actions,
      };
    }
    case "reconciling":
      return {
        tone: "syncing",
        title: "controlMode.syncing",
        detail: "controlMode.syncingDetail",
        actions: ["switch-local"],
      };
    case "conflict-review":
      return {
        tone: "conflict",
        title: "controlMode.conflict",
        detail: "controlMode.conflictDetail",
        actions: ["view-conflicts"],
      };
    default:
      return null;
  }
}

/** 某个模式的短标签，用于事件历史（如「10:31 本地模式」）。 */
export function controlModeLabel(mode: ControlMode): MessageKey {
  switch (mode) {
    case "online":
      return "controlMode.online";
    case "offline-local":
      return "controlMode.local";
    case "reconciling":
      return "controlMode.syncing";
    case "conflict-review":
      return "controlMode.conflict";
  }
}

/** 最近一次迁入 mode 的事件；recent_events 由服务端按时间倒序给。 */
export function newestModeEventTo(
  events: ControlModeEvent[] | undefined,
  mode: ControlMode,
): ControlModeEvent | null {
  for (const event of events ?? []) {
    if (event.to === mode) return event;
  }
  return null;
}

/**
 * 一次性冲突提醒：按**事件 id** 去重，不是按模式去重。
 *
 * 冲突横幅是常驻的（spec §7：常驻横幅 + 一次性通知），但「又冒出冲突了」这件事
 * 只该主动提醒一次：用户关掉之后，只要还是同一次进入就不再打扰；真的走完一轮
 * 又撞上新冲突时服务端会留新事件，新 id 自然会再提醒一次。按模式去重的话，
 * 关掉后下一拍轮询又会弹回来 —— 那等于没有关闭。
 */
export function conflictReminder(
  status: ControlSyncStatus | null | undefined,
  dismissed: ReadonlySet<string>,
): ControlModeEvent | null {
  if (!status || status.mode !== "conflict-review") return null;
  if ((status.conflict_count ?? 0) <= 0) return null;
  const event = newestModeEventTo(status.recent_events, "conflict-review");
  if (!event || dismissed.has(event.id)) return null;
  return event;
}