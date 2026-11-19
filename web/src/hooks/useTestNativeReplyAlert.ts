import { useCallback, useEffect, useState } from "react";
import {
  describeBatteryExemption,
  describeBatterySettingsPage,
  openAppBatterySettings,
  readBatteryExemption,
  requestBatteryExemption,
  testNativeReplyAlert,
} from "../services/net/replyPoller";
import { supportsCustomNativePlugins } from "../services/platform/runtime";

// 组件层不许直接 import services（守卫测试 reviewer-architecture-rules），
// 由 hook 这一层代持；仓库里 useReportError 同例。
// supported = false（iOS 壳）时按钮不许出现：那边没有后台轮询服务可测。
export function useTestNativeReplyAlert(): {
  message: string;
  run: () => Promise<void>;
  supported: boolean;
  batteryIgnoring: boolean | null;
  requestExemption: () => Promise<void>;
  openBatterySettings: () => Promise<void>;
} {
  const [message, setMessage] = useState("");
  const [batteryIgnoring, setBatteryIgnoring] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    void readBatteryExemption().then((value) => {
      if (!cancelled) setBatteryIgnoring(value);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const run = useCallback(async () => {
    const error = await testNativeReplyAlert();
    const battery = describeBatteryExemption(batteryIgnoring);
    setMessage(
      [error || "测试提醒已发送（10 秒后还会再发一条，届时请切到后台看是否会弹出横幅）", battery]
        .filter(Boolean)
        .join("\n"),
    );
  }, [batteryIgnoring]);

  const requestExemption = useCallback(async () => {
    const next = await requestBatteryExemption();
    if (next !== null) {
      setBatteryIgnoring(next);
      setMessage(describeBatteryExemption(next));
    }
  }, []);

  // 国产 ROM 的「允许后台运行 / 应用速冻」名单：AOSP 豁免覆盖不到它，且无 API 可读，
  // 只能跳页面让用户自己放行。返回值只用于告诉用户「跳到哪了」。
  const openBatterySettings = useCallback(async () => {
    const opened = await openAppBatterySettings();
    setMessage(describeBatterySettingsPage(opened));
  }, []);

  return {
    message,
    run,
    supported: supportsCustomNativePlugins(),
    batteryIgnoring,
    requestExemption,
    openBatterySettings,
  };
}
