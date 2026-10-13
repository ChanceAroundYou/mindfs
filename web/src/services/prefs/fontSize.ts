import {
  DEFAULT_FONT_SIZE_PREFERENCES,
  normalizeFontSizeScale,
  type FontSizePreferences,
} from "../../shared/fontSize";
export {
  changeFontSize,
  DEFAULT_FONT_SIZE_PREFERENCES,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  FONT_SIZE_STEP,
} from "../../shared/fontSize";
export type { FontSizePreferences, FontSizeRegion } from "../../shared/fontSize";

const FONT_SIZE_STORAGE_KEY = "mindfs-font-size-preferences";

export function loadFontSizePreferences(): FontSizePreferences {
  if (typeof window === "undefined") {
    return DEFAULT_FONT_SIZE_PREFERENCES;
  }
  try {
    const saved = JSON.parse(window.localStorage.getItem(FONT_SIZE_STORAGE_KEY) || "{}") as Partial<FontSizePreferences>;
    return {
      fileSidebar: normalizeFontSizeScale(saved.fileSidebar),
      main: normalizeFontSizeScale(saved.main),
      sessionSidebar: normalizeFontSizeScale(saved.sessionSidebar),
    };
  } catch {
    return DEFAULT_FONT_SIZE_PREFERENCES;
  }
}

export function persistFontSizePreferences(preferences: FontSizePreferences): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(FONT_SIZE_STORAGE_KEY, JSON.stringify(preferences));
  } catch {
    // The current session can still use the selected sizes when storage is unavailable.
  }
}
