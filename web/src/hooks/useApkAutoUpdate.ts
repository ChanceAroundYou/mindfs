import { useCallback, useEffect, useRef, useState } from "react";
import { checkApkUpdate, installApkUpdate, type ApkRelease } from "../services/update/githubAppUpdate";
import { isAndroidRuntime } from "../services/platform/runtime";

// 自动检查更新：打开 App / 从后台切回前台各看一眼 GitHub 上的最新 APK。
// 只有「有更新」才弹窗，检查失败保持沉默（网络抖动不该打扰用户）。
const AUTO_CHECK_STORAGE_KEY = "mindfs:apk-auto-check-at";
const AUTO_CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;

function readLastCheckAt(): number {
  try {
    return Number(window.localStorage.getItem(AUTO_CHECK_STORAGE_KEY) || "0") || 0;
  } catch {
    return 0;
  }
}

function writeLastCheckAt(value: number): void {
  try {
    window.localStorage.setItem(AUTO_CHECK_STORAGE_KEY, String(value));
  } catch {
    // 隐私模式下 localStorage 可能不可写：只是失去节流，不影响更新。
  }
}

export type ApkAutoUpdate = {
  release: ApkRelease | null;
  message: string;
  busy: boolean;
  apply: () => Promise<void>;
  later: () => void;
};

export function useApkAutoUpdate(): ApkAutoUpdate {
  const [release, setRelease] = useState<ApkRelease | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const checkingRef = useRef(false);

  const check = useCallback(async () => {
    if (checkingRef.current) return;
    if (Date.now() - readLastCheckAt() < AUTO_CHECK_INTERVAL_MS) return;
    checkingRef.current = true;
    try {
      const result = await checkApkUpdate();
      if (result.kind === "available") {
        setRelease(result.release);
        setMessage(`发现新版本 ${result.release.tag}`);
      }
    } catch {
      // 自动检查失败不提示：手动检查（关于弹窗）会给出具体原因。
    } finally {
      writeLastCheckAt(Date.now());
      checkingRef.current = false;
    }
  }, []);

  useEffect(() => {
    if (!isAndroidRuntime()) return;
    void check();
    const onVisible = () => {
      if (document.visibilityState === "visible") void check();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [check]);

  const apply = useCallback(async () => {
    if (!release || busy) return;
    setBusy(true);
    const error = installApkUpdate(release);
    setMessage(error || `正在下载 ${release.tag}，完成后按系统提示安装`);
    setBusy(false);
    if (!error) setRelease(null);
  }, [release, busy]);

  const later = useCallback(() => {
    setRelease(null);
    setMessage("");
  }, []);

  return { release, message, busy, apply, later };
}
