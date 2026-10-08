import { useEffect, useRef, useState } from "react";
import {
  GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY,
  MOBILE_ENTER_KEY_SEND_STORAGE_KEY,
  SIDEBARS_SWAPPED_STORAGE_KEY,
  loadGitDiffSideBySide,
  loadMobileEnterKeySends,
  loadSidebarsSwapped,
} from "./appStorage";
import {
  loadFontSizePreferences,
  persistFontSizePreferences,
  type FontSizePreferences,
} from "../services/prefs/fontSize";
import {
  loadSendShortcut,
  persistSendShortcut,
  type SendShortcut,
} from "../services/prefs/sendShortcut";

/**
 * 纯 UI 偏好：移动端回车发送、发送快捷键、左右栏互换、字号、diff 并排。
 *
 * 每个偏好的持久化 effect 单独保留（不合并成一个 effect）—— 合并会让任一偏好变化
 * 触发全部写入，也会改变 localStorage 的写入次数。三个布尔型偏好沿用 App 原先的
 * 裸 setItem + try/catch（不走 appStorage 的包装），保持行为逐字一致。
 *
 * `slashCopyResetTimersRef` 一并收在这里，因为它的唯一用途就是在卸载时清掉
 * 「已复制」徽标的重置定时器，属于同一块 UI 偏好状态。
 */
export function useUiPreferences() {
  const slashCopyResetTimersRef = useRef<Record<string, number>>({});
  const [mobileEnterKeySends, setMobileEnterKeySends] = useState(loadMobileEnterKeySends);
  const [sendShortcut, setSendShortcut] = useState<SendShortcut | null>(loadSendShortcut);
  const [sidebarsSwapped, setSidebarsSwapped] = useState(loadSidebarsSwapped);
  const [fontSizePreferences, setFontSizePreferences] = useState<FontSizePreferences>(loadFontSizePreferences);
  const [gitDiffSideBySide, setGitDiffSideBySide] = useState(loadGitDiffSideBySide);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        MOBILE_ENTER_KEY_SEND_STORAGE_KEY,
        mobileEnterKeySends ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [mobileEnterKeySends]);

  useEffect(() => {
    persistSendShortcut(sendShortcut);
  }, [sendShortcut]);

  useEffect(() => {
    persistFontSizePreferences(fontSizePreferences);
  }, [fontSizePreferences]);

  useEffect(() => {
    return () => {
      Object.values(slashCopyResetTimersRef.current).forEach((timer) =>
        window.clearTimeout(timer),
      );
      slashCopyResetTimersRef.current = {};
    };
  }, []);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        SIDEBARS_SWAPPED_STORAGE_KEY,
        sidebarsSwapped ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [sidebarsSwapped]);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY,
        gitDiffSideBySide ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [gitDiffSideBySide]);

  return {
    slashCopyResetTimersRef,
    mobileEnterKeySends,
    setMobileEnterKeySends,
    sendShortcut,
    setSendShortcut,
    sidebarsSwapped,
    setSidebarsSwapped,
    fontSizePreferences,
    setFontSizePreferences,
    gitDiffSideBySide,
    setGitDiffSideBySide,
  };
}
