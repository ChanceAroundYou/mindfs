import { useCallback, useState } from "react";
import { getAppearanceMode, setAppearanceMode } from "../../services/prefs/appearance";
import type { AppearanceMode } from "../../shared/appearanceTypes";

export function useFileTreeAppearance() {
  const [appearanceMode, setAppearanceModeState] = useState<AppearanceMode>(() => getAppearanceMode());
  const changeAppearanceMode = useCallback((mode: AppearanceMode) => {
    setAppearanceMode(mode);
    setAppearanceModeState(mode);
  }, []);

  return { appearanceMode, changeAppearanceMode };
}
