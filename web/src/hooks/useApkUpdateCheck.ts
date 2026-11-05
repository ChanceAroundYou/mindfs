import { useCallback, useState } from "react";
import { checkApkUpdate, installApkUpdate } from "../services/update/githubAppUpdate";

// 关于弹窗里的「检查更新」：真比对 GitHub 上的最新 APK，有更新就直接下载并拉起安装器。
export function useApkUpdateCheck() {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const run = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    setMessage("正在检查更新…");
    try {
      const result = await checkApkUpdate();
      if (result.kind === "unknown") {
        setMessage(`无法检查更新：${result.reason}`);
        return;
      }
      if (result.kind === "latest") {
        setMessage("已是最新版本");
        return;
      }
      setMessage(`发现新版本 ${result.release.tag}，正在下载…`);
      const error = installApkUpdate(result.release);
      setMessage(error || `正在下载 ${result.release.tag}，完成后按系统提示安装`);
    } catch (ex) {
      setMessage(`检查更新失败：${ex instanceof Error ? ex.message : String(ex)}`);
    } finally {
      setBusy(false);
    }
  }, [busy]);

  return { busy, message, run };
}
