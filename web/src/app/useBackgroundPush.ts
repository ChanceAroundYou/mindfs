import { useEffect, useRef } from "react";
import { isNativeShellRuntime } from "../services/platform/runtime";
import {
  backgroundPushDeclined,
  ensureWebPushSubscribed,
  needsNotificationPermission,
} from "../services/webPush";

/** 切回前台时补一次订阅对账；60s 内的重复触发直接跳过（每次要问两个端点）。 */
const RECHECK_INTERVAL_MS = 60_000;

/**
 * 让网页版在后台也能收到提醒。
 *
 * 页面隐藏后浏览器会把定时器节流到约 1 次/分钟，几分钟后直接冻结整个页面 ——
 * 冻结之后页面内的 5s 轮询、WebAudio、甚至 fetch 回调都不再执行。Web Push 由
 * 浏览器进程直接投递到 service worker，是唯一与页面生命周期无关的通道。
 *
 * 这里做三件事：
 *  1. 权限已授予（granted）就**静默**订阅，不需要用户去通知面板点开关；
 *  2. 权限还是 default 时，等用户第一次真实交互再弹系统权限框 —— Firefox
 *     强制要求用户手势，加载瞬间弹框也会被 Chrome 判成骚扰；
 *  3. 每次切回前台补一次对账（SW 更新、服务端订阅表丢失都会让订阅失效）。
 *
 * 原生壳不参与：那边由 ReplyPollerService 常驻长轮询。
 */
export function useBackgroundPush() {
  const lastRunRef = useRef(0);

  useEffect(() => {
    if (isNativeShellRuntime()) {
      return;
    }

    const sync = async (prompt: boolean) => {
      const now = Date.now();
      if (!prompt && now - lastRunRef.current < RECHECK_INTERVAL_MS) {
        return;
      }
      lastRunRef.current = now;
      try {
        await ensureWebPushSubscribed({ prompt });
      } catch {
        // 推送是旁路：失败只代表「后台提醒不可用」，不能影响页面本身。
      }
    };

    void sync(false);

    const onVisible = () => {
      if (document.visibilityState === "visible") {
        void sync(false);
      }
    };
    document.addEventListener("visibilitychange", onVisible);

    // 权限框只在用户第一次真实交互时弹；用户拒绝过就再也不弹。
    let removeGesture = () => {};
    if (needsNotificationPermission() && !backgroundPushDeclined()) {
      const ask = () => {
        removeGesture();
        void sync(true);
      };
      removeGesture = () => {
        window.removeEventListener("pointerdown", ask, true);
        window.removeEventListener("keydown", ask, true);
      };
      window.addEventListener("pointerdown", ask, true);
      window.addEventListener("keydown", ask, true);
    }

    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      removeGesture();
    };
  }, []);
}
