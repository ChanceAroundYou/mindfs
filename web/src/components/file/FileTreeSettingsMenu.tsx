import React, { type Dispatch, type SetStateAction } from "react";
import type { I18nContextValue, Locale, MessageKey } from "../../i18n/index";
import type { AppearanceMode } from "../../shared/appearanceTypes";
import { changeFontSize, FONT_SIZE_MAX, FONT_SIZE_MIN, type FontSizeRegion, type FontSizePreferences } from "../../shared/fontSize";
import { formatSendShortcut, type SendShortcut } from "../../shared/sendShortcut";
import type { FileTreeProps } from "./fileTreeTypes";
import { fileTreeMenuButtonStyle, ChevronRight } from "./icons";
import { fontSizeAdjustButtonStyle } from "./fileTreeMenuStyles";
import { DIRECTORY_SORT_OPTIONS } from "../../shared/directorySort";
import { APPEARANCE_OPTIONS, DIRECTORY_SORT_LABEL_KEYS, LOCALE_OPTIONS } from "./fileTreeMenuOptions";
import { FileTreeAppearanceOptions } from "./FileTreeAppearanceOptions";
import { FileTreeFontSizeMenu } from "./FileTreeFontSizeMenu";
import { FileTreeLocaleOptions } from "./FileTreeLocaleOptions";
import { FileTreeSortOptions } from "./FileTreeSortOptions";

type FileTreeSettingsMenuProps = {
  t: I18nContextValue["t"];
  locale: Locale;
  setLocale: (locale: Locale) => void;
  appearanceMode: AppearanceMode;
  changeAppearanceMode: (mode: AppearanceMode) => void;
  isMenuOpen: boolean;
  setIsMenuOpen: Dispatch<SetStateAction<boolean>>;
  isAppearanceMenuOpen: boolean;
  setIsAppearanceMenuOpen: Dispatch<SetStateAction<boolean>>;
  isFontSizeMenuOpen: boolean;
  setIsFontSizeMenuOpen: Dispatch<SetStateAction<boolean>>;
  isLocaleMenuOpen: boolean;
  setIsLocaleMenuOpen: Dispatch<SetStateAction<boolean>>;
  isSortMenuOpen: boolean;
  setIsSortMenuOpen: Dispatch<SetStateAction<boolean>>;
  sortMode: FileTreeProps["sortMode"];
  onSortModeChange: FileTreeProps["onSortModeChange"];
  fontSizePreferences: FontSizePreferences;
  onFontSizePreferencesChange: FileTreeProps["onFontSizePreferencesChange"];
  gitDiffSideBySide: boolean;
  onGitDiffSideBySideChange: FileTreeProps["onGitDiffSideBySideChange"];
  showEnterKeySendOption: boolean;
  enterKeySends: boolean;
  onEnterKeySendsChange: FileTreeProps["onEnterKeySendsChange"];
  showSendShortcutOption: boolean;
  sendShortcut: SendShortcut | null;
  openSendShortcut: () => void;
  newProjectMetaLocation: "home" | "project";
  newProjectMetaLocationBusy: boolean;
  toggleNewProjectMetaLocation: () => void | Promise<void>;
};

export function FileTreeSettingsMenu({
  t, locale, setLocale, appearanceMode, changeAppearanceMode,
  isMenuOpen, setIsMenuOpen, isAppearanceMenuOpen, setIsAppearanceMenuOpen,
  isFontSizeMenuOpen, setIsFontSizeMenuOpen, isLocaleMenuOpen, setIsLocaleMenuOpen,
  isSortMenuOpen, setIsSortMenuOpen, sortMode, onSortModeChange,
  fontSizePreferences, onFontSizePreferencesChange, gitDiffSideBySide,
  onGitDiffSideBySideChange, showEnterKeySendOption, enterKeySends,
  onEnterKeySendsChange, showSendShortcutOption, sendShortcut, openSendShortcut,
  newProjectMetaLocation, newProjectMetaLocationBusy, toggleNewProjectMetaLocation,
}: FileTreeSettingsMenuProps) {
  const sortLabel = (mode: FileTreeProps["sortMode"]): string => {
    const key = DIRECTORY_SORT_LABEL_KEYS[mode];
    return key ? t(key) : t("directory.defaultSort");
  };
  const fontSizeRows: Array<{ region: FontSizeRegion; labelKey: MessageKey }> = [
    { region: "fileSidebar", labelKey: "fileTree.fontSizeFileSidebar" },
    { region: "main", labelKey: "fileTree.fontSizeMain" },
    { region: "sessionSidebar", labelKey: "fileTree.fontSizeSessionSidebar" },
  ];

  return <>
                <button
                  type="button"
                  onClick={() => {
                    setIsAppearanceMenuOpen((open) => !open);
                    setIsFontSizeMenuOpen(false);
                    setIsLocaleMenuOpen(false);
                    setIsSortMenuOpen(false);
                  }}
                  style={{
                    width: "100%",
                    border: "none",
                    background: "transparent",
                    color: "var(--text-primary)",
                    borderRadius: "8px",
                    padding: "8px 10px",
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    textAlign: "left",
                    cursor: "pointer",
                    fontSize: "12px",
                  }}
                  aria-expanded={isAppearanceMenuOpen}
                >
                  <span style={{ flex: 1 }}>{t("appearance.title")}</span>
                  <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                    {t(APPEARANCE_OPTIONS.find((option) => option.value === appearanceMode)?.labelKey || "appearance.system")}
                  </span>
                  <ChevronRight isOpen={isAppearanceMenuOpen} />
                </button>
<FileTreeAppearanceOptions open={isAppearanceMenuOpen} options={APPEARANCE_OPTIONS} value={appearanceMode} onChange={changeAppearanceMode} closeMenus={() => { setIsMenuOpen(false); setIsAppearanceMenuOpen(false); setIsLocaleMenuOpen(false); setIsSortMenuOpen(false); }} t={t} />
                <button
                  type="button"
                  onClick={() => {
                    setIsFontSizeMenuOpen((open) => !open);
                    setIsAppearanceMenuOpen(false);
                    setIsLocaleMenuOpen(false);
                    setIsSortMenuOpen(false);
                  }}
                  style={fileTreeMenuButtonStyle}
                  aria-expanded={isFontSizeMenuOpen}
                >
                  <span style={{ flex: 1 }}>{t("fileTree.fontSize")}</span>
                  <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                    {t("fileTree.fontSizeByRegion")}
                  </span>
                  <ChevronRight isOpen={isFontSizeMenuOpen} />
                </button>
                <FileTreeFontSizeMenu open={isFontSizeMenuOpen} rows={fontSizeRows} prefs={fontSizePreferences} min={FONT_SIZE_MIN} max={FONT_SIZE_MAX} onChange={onFontSizePreferencesChange} changeFontSize={changeFontSize} buttonStyle={fontSizeAdjustButtonStyle} t={t} />
                <button
                  type="button"
                  onClick={() => {
                    setIsLocaleMenuOpen((open) => !open);
                    setIsAppearanceMenuOpen(false);
                    setIsFontSizeMenuOpen(false);
                    setIsSortMenuOpen(false);
                  }}
                  style={{
                    width: "100%",
                    border: "none",
                    background: "transparent",
                    color: "var(--text-primary)",
                    borderRadius: "8px",
                    padding: "8px 10px",
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    textAlign: "left",
                    cursor: "pointer",
                    fontSize: "12px",
                  }}
                  aria-expanded={isLocaleMenuOpen}
                >
                  <span style={{ flex: 1 }}>{t("locale.language")}</span>
                  <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                    {t(LOCALE_OPTIONS.find((option) => option.value === locale)?.labelKey || "locale.zhCN")}
                  </span>
                  <ChevronRight isOpen={isLocaleMenuOpen} />
                </button>
                <FileTreeLocaleOptions open={isLocaleMenuOpen} options={LOCALE_OPTIONS} locale={locale} setLocale={setLocale} closeMenus={() => { setIsMenuOpen(false); setIsAppearanceMenuOpen(false); setIsLocaleMenuOpen(false); setIsSortMenuOpen(false); }} t={t} />
                <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />
                <button
                  type="button"
                  onClick={() => {
                    setIsSortMenuOpen((open) => !open);
                    setIsAppearanceMenuOpen(false);
                    setIsFontSizeMenuOpen(false);
                    setIsLocaleMenuOpen(false);
                  }}
                  style={{
                    width: "100%",
                    border: "none",
                    background: "transparent",
                    color: "var(--text-primary)",
                    borderRadius: "8px",
                    padding: "8px 10px",
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    textAlign: "left",
                    cursor: "pointer",
                    fontSize: "12px",
                  }}
                  aria-expanded={isSortMenuOpen}
                >
                  <span style={{ flex: 1 }}>{t("fileTree.globalSort")}</span>
                  <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                    {sortLabel(sortMode)}
                  </span>
                  <ChevronRight isOpen={isSortMenuOpen} />
                </button>
<FileTreeSortOptions open={isSortMenuOpen} options={DIRECTORY_SORT_OPTIONS} value={sortMode} onChange={onSortModeChange} closeMenus={() => { setIsMenuOpen(false); setIsAppearanceMenuOpen(false); setIsLocaleMenuOpen(false); setIsSortMenuOpen(false); }} sortLabel={sortLabel} />
              <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />
              <button
                type="button"
                onClick={() => {
                  onGitDiffSideBySideChange?.(!gitDiffSideBySide);
                  setIsAppearanceMenuOpen(false);
                  setIsSortMenuOpen(false);
                }}
                style={{
                  width: "100%",
                  border: "none",
                  background: gitDiffSideBySide ? "var(--selection-bg)" : "transparent",
                  color: gitDiffSideBySide ? "var(--accent-color)" : "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <span>{t("fileTree.sideBySideDiff")}</span>
                <span style={{ fontSize: "11px", opacity: gitDiffSideBySide ? 1 : 0 }}>✓</span>
              </button>
              {showEnterKeySendOption ? (
                <button
                  type="button"
                  onClick={() => {
                    onEnterKeySendsChange?.(!enterKeySends);
                    setIsAppearanceMenuOpen(false);
                    setIsSortMenuOpen(false);
                  }}
                  style={{
                    width: "100%",
                    border: "none",
                    background: enterKeySends ? "var(--selection-bg)" : "transparent",
                    color: enterKeySends ? "var(--accent-color)" : "var(--text-primary)",
                    borderRadius: "8px",
                    padding: "8px 10px",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    textAlign: "left",
                    cursor: "pointer",
                    fontSize: "12px",
                  }}
                >
                  <span>{t("fileTree.enterKeySend")}</span>
                  <span style={{ fontSize: "11px", opacity: enterKeySends ? 1 : 0 }}>✓</span>
                </button>
              ) : null}
              {showSendShortcutOption ? (
                <button type="button" onClick={openSendShortcut} style={fileTreeMenuButtonStyle}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <rect x="3" y="5" width="18" height="14" rx="2" />
                    <path d="M7 9h.01M11 9h.01M15 9h.01M7 13h.01M11 13h6" />
                  </svg>
                  <span style={{ flex: 1 }}>{t("fileTree.sendShortcut")}</span>
                  <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                    {sendShortcut ? formatSendShortcut(sendShortcut) : t("sendShortcut.notSet")}
                  </span>
                </button>
              ) : null}
              <button
                type="button"
                disabled={newProjectMetaLocationBusy}
                onClick={() => { void toggleNewProjectMetaLocation(); }}
                style={{
                  width: "100%",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  textAlign: "left",
                  cursor: newProjectMetaLocationBusy ? "default" : "pointer",
                  fontSize: "12px",
                  opacity: newProjectMetaLocationBusy ? 0.65 : 1,
                }}
              >
                <span style={{ flex: 1 }}>{t("fileTree.newProjectMetaLocation")}</span>
                <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                  {t(newProjectMetaLocation === "home" ? "fileTree.metaLocationHome" : "fileTree.metaLocationProject")}
                </span>
              </button>
  </>;
}
