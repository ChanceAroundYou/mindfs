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
 * - error：下行 A3→E3，闷、拖尾（出事了）
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
    { freq: 220.0, at: 0, dur: 0.22, type: "sine", gain: 1.1 },
    { freq: 164.8, at: 0.16, dur: 0.34, type: "sine", gain: 1.1 },
  ],
};

export function useNotificationSound() {
  const audioContextRef = useRef<AudioContext | null>(null);
  const unlockedRef = useRef(false);

  const ensureAudioContext = useCallback((): AudioContext | null => {
    if (typeof window === "undefined") return null;
    const Ctor =
      window.AudioContext ||
      (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!Ctor) return null;
    if (!audioContextRef.current) audioContextRef.current = new Ctor();
    return audioContextRef.current;
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const unlock = () => {
      const ctx = ensureAudioContext();
      if (!ctx) return;
      if (ctx.state === "running") { unlockedRef.current = true; return; }
      void ctx.resume().then(() => { unlockedRef.current = ctx.state === "running"; }).catch(() => {});
    };
    const opts: AddEventListenerOptions = { passive: true };
    window.addEventListener("pointerdown", unlock, opts);
    window.addEventListener("keydown", unlock, opts);
    window.addEventListener("touchstart", unlock, opts);
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
      window.removeEventListener("touchstart", unlock);
    };
  }, [ensureAudioContext]);

  const play = useCallback((event: NotifyEvent) => {
    const settings = loadSettings();
    if (!settings.enabled || !settings.events[event]) return;
    const ctx = ensureAudioContext();
    if (!ctx || ctx.state !== "running") return;
    unlockedRef.current = true;
    const notes = SCORES[event];
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
  }, [ensureAudioContext]);

  return { play };
}

export { loadSettings as loadNotificationSettings, DEFAULTS as notificationSoundDefaults };
