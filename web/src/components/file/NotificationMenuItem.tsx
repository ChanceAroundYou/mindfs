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
 */
export function NotificationMenuItem() {
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
        onClick={() => setOpen((v) => !v)}
        style={{ ...fileTreeMenuButtonStyle, color: soundOn ? "var(--accent-color)" : "var(--text-primary)" }}
      >
        <NotificationIcon />
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {t("fileTree.notificationPanel")}
        </span>
        <span style={{ marginLeft: "auto", display: "inline-flex", transform: open ? "rotate(90deg)" : "none", transition: "transform 0.15s ease" }}>
          <ChevronRight isOpen={open} />
        </span>
      </button>

      {open ? (
        <div style={{ padding: "0 10px 8px 32px", display: "flex", flexDirection: "column", gap: "6px" }}>
          <label style={{ display: "flex", alignItems: "center", gap: "6px", fontSize: "12px" }}>
            <input
              type="checkbox"
              checked={soundOn}
              onChange={(e) => save({ ...settings, enabled: e.target.checked })}
            />
            {t("fileTree.soundEnabled")}
          </label>

          <label style={{ display: "flex", alignItems: "center", gap: "6px", fontSize: "11px", color: "var(--text-secondary)" }}>
            {t("fileTree.soundVolume")}
            <input
              type="range"
              min={0}
              max={1}
              step={0.01}
              value={settings.volume}
              disabled={!soundOn}
              onChange={(e) => save({ ...settings, volume: Number(e.target.value) })}
              style={{ flex: 1, minWidth: 0 }}
            />
          </label>

          {EVENT_KEYS.map(({ event, key }) => (
            <label key={event} style={{ display: "flex", alignItems: "center", gap: "6px", fontSize: "12px", opacity: soundOn ? 1 : 0.5 }}>
              <input
                type="checkbox"
                checked={settings.events[event]}
                disabled={!soundOn}
                onChange={(e) => save({ ...settings, events: { ...settings.events, [event]: e.target.checked } })}
              />
              <span style={{ flex: 1 }}>{t(key)}</span>
              <button
                type="button"
                disabled={!soundOn || !settings.events[event]}
                onClick={() => play(event)}
                style={{
                  border: "none",
                  background: "transparent",
                  color: "var(--accent-color)",
                  cursor: "pointer",
                  fontSize: "11px",
                  padding: 0,
                }}
              >
                {t("fileTree.soundTest")}
              </button>
            </label>
          ))}

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
              padding: 0,
            }}
          >
            {t("fileTree.soundReset")}
          </button>

          <div style={{ height: "1px", background: "var(--border-color)", margin: "2px 0" }} />
          <div style={{ margin: "0 -10px 0 -32px" }}>
            <WebPushMenuItem />
          </div>
        </div>
      ) : null}
    </div>
  );
}
