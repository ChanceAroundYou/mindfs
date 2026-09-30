import { useEffect, useState } from "react";
import {
  APPEARANCE_CHANGE_EVENT,
  getAppearanceMode,
  getEffectiveAppearanceMode,
} from "../../services/appearance";

/** 跟随显式设置 + 系统亮暗偏好的 isDark 订阅。 */
export function useAppearanceSync(): boolean {
  const [isDark, setIsDark] = useState(() => getEffectiveAppearanceMode() === "dark");

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const syncAppearance = () => setIsDark(getEffectiveAppearanceMode() === "dark");
    const onSystemChange = () => {
      if (getAppearanceMode() === "system") {
        syncAppearance();
      }
    };
    window.addEventListener(APPEARANCE_CHANGE_EVENT, syncAppearance);
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", onSystemChange);
      return () => {
        window.removeEventListener(APPEARANCE_CHANGE_EVENT, syncAppearance);
        media.removeEventListener("change", onSystemChange);
      };
    }
    media.addListener(onSystemChange);
    return () => {
      window.removeEventListener(APPEARANCE_CHANGE_EVENT, syncAppearance);
      media.removeListener(onSystemChange);
    };
  }, []);

  return isDark;
}
