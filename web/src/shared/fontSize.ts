export type FontSizeRegion = "fileSidebar" | "main" | "sessionSidebar";
export type FontSizePreferences = Record<FontSizeRegion, number>;

export const DEFAULT_FONT_SIZE_PREFERENCES: FontSizePreferences = {
  fileSidebar: 1,
  main: 1,
  sessionSidebar: 1,
};

export const FONT_SIZE_MIN = 0.8;
export const FONT_SIZE_MAX = 1.4;
export const FONT_SIZE_STEP = 0.1;

export function normalizeFontSizeScale(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return 1;
  const stepped = Math.round(value / FONT_SIZE_STEP) * FONT_SIZE_STEP;
  return Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, Number(stepped.toFixed(1))));
}

export function changeFontSize(
  preferences: FontSizePreferences,
  region: FontSizeRegion,
  direction: -1 | 1,
): FontSizePreferences {
  return {
    ...preferences,
    [region]: normalizeFontSizeScale(preferences[region] + direction * FONT_SIZE_STEP),
  };
}
