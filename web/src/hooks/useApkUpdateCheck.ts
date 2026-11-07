import { useCallback, useState } from "react";
import { checkApkUpdate, installApkUpdate } from "../services/update/githubAppUpdate";
import { isAndroidRuntime } from "../services/platform/runtime";

// 关于弹窗里的「检查更新」：真比对 GitHub 上的最新 APK，有更新就直接下载并拉起安装器。
// supported = false 时按钮不许出现：装 APK 这条通道只有安卓壳有（iOS 要重新签名重装，见 docs/ios-shell.md），
// 让 iOS 去按这个按钮只会得到「正在下载…请按系统提示安装」这种做不到的承诺。
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

  return { busy, message, run, supported: isAndroidRuntime() };
}
