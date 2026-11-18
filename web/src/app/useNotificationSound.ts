import { useCallback, useEffect, useRef } from "react";

export type NotifyEvent = "complete" | "intervene" | "error";

export type SoundSettings = {
  enabled: boolean;
  volume: number;
  events: Record<NotifyEvent, boolean>;
};

const STORAGE_KEY = "mindfs.notify.sound";

const DEFAULTS: SoundSettings = {
  enabled: true,
  volume: 0.22,
  events: { complete: true, intervene: true, error: true },
};

function loadSettings(): SoundSettings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<SoundSettings>;
    return {
      enabled: parsed.enabled ?? DEFAULTS.enabled,
      volume: typeof parsed.volume === "number" ? parsed.volume : DEFAULTS.volume,
      events: { ...DEFAULTS.events, ...(parsed.events || {}) },
    };
  } catch {
    return DEFAULTS;
  }
}

export function saveNotificationSettings(next: SoundSettings) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch {}
}

/**
 * 三段音色的差别必须在「不看屏幕」时也分得清，所以用**音高 + 节奏**两个维度：
 * - complete：C6→E6→G6 上行三连，明亮、收束感（任务做完了）
 * - intervene：A5 两次短提示，间隔明显（叫你看一眼）
 * - error：F5→Bb4 下行纯四度 + 长拖尾，中音区、带棱角（出事了）
 *   —— 曾经是 A3→E3 的低闷音，实测在笔记本小喇叭上几乎听不见，且和系统「咚」混在一起；
 *   换成中音区的三角波既听得见，又靠**下行**与完成音的上行形成方向对比。
 * 每个音符都是独立 oscillator，带 8ms 起音与指数衰减，避免爆音。
 */
type Note = { freq: number; at: number; dur: number; type: OscillatorType; gain?: number };

const SCORES: Record<NotifyEvent, Note[]> = {
  complete: [
    { freq: 1046.5, at: 0, dur: 0.14, type: "sine" },
    { freq: 1318.5, at: 0.09, dur: 0.14, type: "sine" },
    { freq: 1568.0, at: 0.18, dur: 0.26, type: "sine" },
  ],
  intervene: [
    { freq: 880.0, at: 0, dur: 0.16, type: "triangle" },
    { freq: 880.0, at: 0.24, dur: 0.16, type: "triangle" },
  ],
  error: [
    { freq: 698.46, at: 0, dur: 0.15, type: "triangle" },
    { freq: 466.16, at: 0.17, dur: 0.42, type: "triangle", gain: 1.15 },
  ],
};

/** 模块级共享 AudioContext：这个 hook 有 5 处调用点，每处建一个会撞浏览器
 *  的上下文上限，也让「后台恢复」要逐个去 resume。一个就够。 */
let sharedAudioContext: AudioContext | null = null;

function ensureSharedAudioContext(): AudioContext | null {
  if (typeof window === "undefined") return null;
  const Ctor =
    window.AudioContext ||
    (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctor) return null;
  if (!sharedAudioContext) sharedAudioContext = new Ctor();
  return sharedAudioContext;
}

export function useNotificationSound() {
  const unlockedRef = useRef(false);

  const ensureAudioContext = useCallback((): AudioContext | null => ensureSharedAudioContext(), []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const unlock = () => {
      const ctx = ensureAudioContext();
      if (!ctx) return;
      if (ctx.state === "running") { unlockedRef.current = true; return; }
      void ctx.resume().then(() => { unlockedRef.current = ctx.state === "running"; }).catch(() => {});
    };
    // 后台标签页会被浏览器挂起音频上下文（移动端尤其如此）。回到前台时先 resume，
    // 否则后台期间排到的那条提示音会静默丢弃 —— 用户听到的是「切回来才响」，
    // 实际上那条提醒早就到了。
    const resumeOnVisible = () => {
      if (document.visibilityState === "visible") unlock();
    };
    const opts: AddEventListenerOptions = { passive: true };
    window.addEventListener("pointerdown", unlock, opts);
    window.addEventListener("keydown", unlock, opts);
    window.addEventListener("touchstart", unlock, opts);
    document.addEventListener("visibilitychange", resumeOnVisible);
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
      window.removeEventListener("touchstart", unlock);
      document.removeEventListener("visibilitychange", resumeOnVisible);
    };
  }, [ensureAudioContext]);

  const play = useCallback((event: NotifyEvent) => {
    const settings = loadSettings();
    if (!settings.enabled || !settings.events[event]) return;
    const ctx = ensureAudioContext();
    if (!ctx) return;

    const schedule = () => {
      const notes = SCORES[event];
      unlockedRef.current = true;
      try {
        const now = ctx.currentTime;
        for (const note of notes) {
          const osc = ctx.createOscillator();
          const gain = ctx.createGain();
          const peak = Math.max(0.0001, settings.volume * (note.gain ?? 1));
          osc.type = note.type;
          osc.frequency.setValueAtTime(note.freq, now + note.at);
          gain.gain.setValueAtTime(0.0001, now + note.at);
          gain.gain.exponentialRampToValueAtTime(peak, now + note.at + 0.008);
          gain.gain.exponentialRampToValueAtTime(0.0001, now + note.at + note.dur);
          osc.connect(gain);
          gain.connect(ctx.destination);
          osc.start(now + note.at);
          osc.stop(now + note.at + note.dur + 0.02);
        }
      } catch (e) {
        if (unlockedRef.current) console.error("Failed to play notification sound:", e);
      }
    };

    if (ctx.state === "running") {
      schedule();
      return;
    }
    // 被挂起时先尝试恢复再排音：桌面浏览器允许在没有新手势的情况下恢复
    // 一个此前被用户手势解锁过的上下文；恢复不了就放弃（不制造噪音）。
    void ctx.resume()
      .then(() => { if (ctx.state === "running") schedule(); })
      .catch(() => {});
  }, [ensureAudioContext]);

  return { play };
}

export { loadSettings as loadNotificationSettings, DEFAULTS as notificationSoundDefaults };
