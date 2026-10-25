import React, { useEffect, useState } from "react";
import { useI18n } from "../../i18n/index";
import {
  useNotificationSound,
  loadNotificationSettings,
  saveNotificationSettings,
  notificationSoundDefaults,
  type NotifyEvent,
  type SoundSettings,
} from "../../app/useNotificationSound";
import { fileTreeMenuButtonStyle, ChevronRight, NotificationIcon, WebPushMenuItem } from "./icons";

const EVENT_KEYS: Array<{ event: NotifyEvent; key: "fileTree.soundComplete" | "fileTree.soundIntervene" | "fileTree.soundError" }> = [
  { event: "complete", key: "fileTree.soundComplete" },
  { event: "intervene", key: "fileTree.soundIntervene" },
  { event: "error", key: "fileTree.soundError" },
];

/**
 * 侧栏 ⋯ 菜单里的「通知」面板：提示音设置 + PWA 推送订阅。
 *
 * 两者合并在同一项下是有意的 —— 用户说的「通知」是同一件事的两个通道：
 * 我在看屏幕时用提示音，不在时用系统推送。分成两项就得让用户自己记住
 * 「声音在哪、推送在哪」，而它们本来就该一起调。
 *
 * 版式沿用 FileTreeFontSizeMenu：外层 `padding: 2px 6px 6px`、每行 `minHeight 30px`、
 * 12px 字号、次要信息用 `var(--text-secondary)`。别在这里另起一套缩进与行高 ——
 * 展开项之间只要有一个盒子不一样，整张菜单看上去就是拼的。
 */
export function NotificationMenuItem({
  onOpen,
  showPush = true,
}: {
  /** 展开时通知父级收起其它展开项（菜单里同时只展开一项）。 */
  onOpen?: () => void;
  /** 原生壳里没有 Web Push，只保留提示音。 */
  showPush?: boolean;
} = {}) {
  const { t } = useI18n();
  const { play } = useNotificationSound();
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<SoundSettings>(() => loadNotificationSettings());

  // 设置可能被另一个标签页改掉：storage 事件是唯一的跨标签页同步信号。
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === "mindfs.notify.sound") setSettings(loadNotificationSettings());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const save = (next: SoundSettings) => {
    setSettings(next);
    saveNotificationSettings(next);
  };

  const soundOn = settings.enabled;

  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setOpen((v) => !v);
          // 与「外观/字号/语言/排序」同一约定：同时只展开一项，否则两套子行并排出现，行高各说各话。
          if (!open) onOpen?.();
        }}
        aria-expanded={open}
        style={fileTreeMenuButtonStyle}
      >
        <NotificationIcon />
        <span style={{ flex: 1 }}>{t("fileTree.notificationPanel")}</span>
        <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
          {t(soundOn ? "fileTree.soundStateOn" : "fileTree.soundStateOff")}
        </span>
        <ChevronRight isOpen={open} />
      </button>

      {open ? (
        <div style={{ padding: "2px 6px 6px", display: "flex", flexDirection: "column", gap: "3px" }}>
          <label style={rowStyle}>
            <input
              type="checkbox"
              checked={soundOn}
              onChange={(e) => save({ ...settings, enabled: e.target.checked })}
            />
            <span style={{ flex: 1 }}>{t("fileTree.soundEnabled")}</span>
          </label>

          <div style={{ ...rowStyle, opacity: soundOn ? 1 : 0.5 }}>
            <span style={{ flex: 1 }}>{t("fileTree.soundVolume")}</span>
            <span style={pillStyle}>
              <input
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={settings.volume}
                disabled={!soundOn}
                onChange={(e) => save({ ...settings, volume: Number(e.target.value) })}
                aria-label={t("fileTree.soundVolume")}
                style={{ width: "72px", accentColor: "var(--accent-color)", cursor: "pointer" }}
              />
              <span style={{ color: "var(--text-secondary)", fontSize: "11px", fontVariantNumeric: "tabular-nums" }}>
                {Math.round(settings.volume * 100)}%
              </span>
            </span>
          </div>

          <div style={{ height: "1px", background: "var(--border-color)", margin: "3px 4px" }} />

          {EVENT_KEYS.map(({ event, key }) => {
            const on = soundOn && settings.events[event];
            return (
              <label key={event} style={{ ...rowStyle, opacity: soundOn ? 1 : 0.5 }}>
                <input
                  type="checkbox"
                  checked={settings.events[event]}
                  disabled={!soundOn}
                  onChange={(e) => save({ ...settings, events: { ...settings.events, [event]: e.target.checked } })}
                />
                <span style={{ flex: 1 }}>{t(key)}</span>
                <button
                  type="button"
                  disabled={!on}
                  onClick={(e) => {
                    // label 里的按钮：不拦就会连带把复选框翻掉。
                    e.preventDefault();
                    play(event);
                  }}
                  style={{
                    flexShrink: 0,
                    border: "none",
                    background: "transparent",
                    color: on ? "var(--accent-color)" : "var(--text-secondary)",
                    cursor: on ? "pointer" : "default",
                    fontSize: "11px",
                    padding: "0 2px",
                  }}
                >
                  {t("fileTree.soundTest")}
                </button>
              </label>
            );
          })}

          <button
            type="button"
            onClick={() => save({ ...notificationSoundDefaults })}
            style={{
              alignSelf: "flex-start",
              border: "none",
              background: "transparent",
              color: "var(--text-secondary)",
              cursor: "pointer",
              fontSize: "11px",
              padding: "0 4px",
              minHeight: "24px",
            }}
          >
            {t("fileTree.soundReset")}
          </button>

          {showPush ? (
            <>
              <div style={{ height: "1px", background: "var(--border-color)", margin: "3px 4px" }} />
              <WebPushMenuItem />
            </>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

const rowStyle: React.CSSProperties = {
  minHeight: "30px",
  padding: "0 4px",
  display: "flex",
  alignItems: "center",
  gap: "6px",
  color: "var(--text-primary)",
  fontSize: "12px",
};

/** 与「字号」那排 −/百分比/+ 同一个胶囊外形，菜单里两处数值控件长得一样。 */
const pillStyle: React.CSSProperties = {
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  gap: "6px",
  border: "1px solid var(--border-color)",
  borderRadius: "7px",
  padding: "2px 8px",
};
