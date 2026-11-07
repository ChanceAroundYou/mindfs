import { useEffect, useState } from "react";
import { getAppInfo } from "../services/update/appUpdate";

// 壳里显示的版本号。安卓走 MindFSAppInfo 桥，iOS 走官方 @capacitor/app
// （CFBundleShortVersionString + CURRENT_PROJECT_VERSION）。
// 关于弹窗原本只读安卓那个同步桥，iOS 上因此只能显示写死的 "web (…)" —— 那是句假话。
export function useNativeShellAppVersion(): string {
  const [version, setVersion] = useState("");
  useEffect(() => {
    let alive = true;
    getAppInfo()
      .then((info) => {
        if (alive && info.version) {
          setVersion(info.version);
        }
      })
      .catch(() => {
        // 拿不到版本就退回 "web"，不弹错误、不阻断弹窗。
      });
    return () => {
      alive = false;
    };
  }, []);
  return version;
}
