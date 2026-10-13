import type { AppearanceMode } from "../../shared/appearanceTypes";
import type { Locale, MessageKey } from "../../i18n/index";
import type { DirectorySortMode } from "../../shared/directorySort";

export const APPEARANCE_OPTIONS: Array<{ value: AppearanceMode; labelKey: MessageKey }> = [
  { value: "dark", labelKey: "appearance.dark" },
  { value: "light", labelKey: "appearance.light" },
  { value: "meadow", labelKey: "appearance.meadow" },
  { value: "moss", labelKey: "appearance.moss" },
  { value: "system", labelKey: "appearance.system" },
];
export const LOCALE_OPTIONS: Array<{ value: Locale; labelKey: MessageKey }> = [
  { value: "zh-CN", labelKey: "locale.zhCN" },
  { value: "en-US", labelKey: "locale.enUS" },
];
export const DIRECTORY_SORT_LABEL_KEYS: Partial<Record<DirectorySortMode, MessageKey>> = {
  "name-asc": "sort.nameAsc", "name-desc": "sort.nameDesc", "mtime-desc": "sort.mtimeDesc",
  "mtime-asc": "sort.mtimeAsc", "size-desc": "sort.sizeDesc", "size-asc": "sort.sizeAsc",
};
