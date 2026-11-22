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

export type ControlSyncStatus = {
  mode: ControlMode;
  base_rev?: number;
  last_primary_at?: string;
  last_error?: string;
};

export type ControlModeBannerState = {
  tone: "local" | "syncing" | "conflict";
  title: MessageKey;
  detail: MessageKey;
  action: "retry" | "switch-local" | "view-conflicts";
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
 * 四态到横幅的纯映射：在线与「没有这个能力」都不出横幅。
 *
 * 抽成纯函数是为了让「哪个状态配哪个按钮」能被用例直接钉住 —— 渲染层只照着画，
 * 不再自己 if/else 一遍，避免两个地方对同一个状态给出不同结论。
 */
export function controlModeBanner(mode: ControlMode | null | undefined): ControlModeBannerState | null {
  switch (mode) {
    case "offline-local":
      return {
        tone: "local",
        title: "controlMode.local",
        detail: "controlMode.localDetail",
        action: "retry",
      };
    case "reconciling":
      return {
        tone: "syncing",
        title: "controlMode.syncing",
        detail: "controlMode.syncingDetail",
        action: "switch-local",
      };
    case "conflict-review":
      return {
        tone: "conflict",
        title: "controlMode.conflict",
        detail: "controlMode.conflictDetail",
        action: "view-conflicts",
      };
    default:
      return null;
  }
}