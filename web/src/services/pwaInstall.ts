/**
 * PWA 安装提示的进程级单例。
 *
 * 为什么不能放在组件里：beforeinstallprompt 每个页面生命周期只派发一次，
 * 而承载它的 FileTree 在移动端会被整块卸载（AppShell 只在侧栏打开时渲染 <aside>），
 * 侧栏一关一开就是全新实例、组件 state 归零，而那个一次性事件不会重放。
 * 结果是每次打开侧栏都先按「还没结论」画一帧、再被清掉——按钮闪一下又消失。
 *
 * 这里在模块首次 import 时就挂上监听，把 event 存过组件的生命周期，
 * 组件只订阅快照，卸载重挂都读得到同一个值。
 */
import { shouldEnablePWAInstall } from "./runtime";

export type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
};

export type PwaInstallSnapshot = {
  /** 浏览器给的原生安装 event；每个页面生命周期只有一次。 */
  deferredPrompt: BeforeInstallPromptEvent | null;
  /** 是否已安装（display-mode: standalone / iOS standalone / 已持久化）。 */
  installed: boolean;
  /**
   * 判定是否已落定。false 表示「浏览器还没告诉我能不能装」，
   * 此时不能把「还没结论」当成「不能装」去渲染——那正是闪烁的来源。
   */
  probed: boolean;
};

type PwaInstallListener = (snapshot: PwaInstallSnapshot) => void;

const INSTALLED_KEY = "mindfs-pwa-installed";

/**
 * 判定落定的兜底时限（毫秒）。
 *
 * beforeinstallprompt 不是所有浏览器都会派发：iOS Safari、桌面版已装、或 Chrome
 * 因启发式（近期拒绝过、engagement 不足）压住时，它可能永远不来。没有这个超时，
 * 「等判定」就等于「永久不显示」。超时后按「没有原生 event」落定，按钮照常出现
 * 一次并保持——宁可晚出现，不可反复翻转，也不可永不出现。
 */
const PROBE_TIMEOUT_MS = 5000;

function isStandaloneDisplay(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  return window.matchMedia("(display-mode: standalone)").matches
    || window.matchMedia("(display-mode: window-controls-overlay)").matches
    || window.matchMedia("(display-mode: fullscreen)").matches
    || (window.navigator as Navigator & { standalone?: boolean }).standalone === true;
}

function hasPersistedInstallState(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(INSTALLED_KEY) === "true";
  } catch {
    return false;
  }
}

function persistInstallState(): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(INSTALLED_KEY, "true");
  } catch {
  }
}

class PwaInstallService {
  private state: PwaInstallSnapshot = {
    deferredPrompt: null,
    installed: false,
    // 装得了的平台：还没拿到 beforeinstallprompt，结论未定。
    // 原生壳 / 已装 这类同步能定论的走 constructor 里的提前返回，不走这个初值。
    probed: false,
  };
  private listeners = new Set<PwaInstallListener>();
  private handleBeforeInstallPrompt: (event: Event) => void = () => {};
  private handleInstalled: () => void = () => {};
  private refreshInstalled: () => void = () => {};
  private probeTimer: ReturnType<typeof setTimeout> | null = null;

  constructor() {
    if (typeof window === "undefined" || !shouldEnablePWAInstall()) {
      this.state = { deferredPrompt: null, installed: false, probed: true };
      return;
    }
    this.state.installed = isStandaloneDisplay() || hasPersistedInstallState();

    this.handleBeforeInstallPrompt = (event: Event) => {
      event.preventDefault();
      this.settleProbe();
      this.patch({ deferredPrompt: event as BeforeInstallPromptEvent, probed: true });
    };
    this.handleInstalled = () => {
      this.settleProbe();
      persistInstallState();
      this.patch({ installed: true, deferredPrompt: null, probed: true });
    };
    // 运行形态变化（用户从标签页切到已安装窗口）只刷新 installed，
    // 不碰 deferredPrompt，避免把已落定的结论翻回去。
    this.refreshInstalled = () => {
      this.patch({ installed: isStandaloneDisplay() || hasPersistedInstallState() });
    };

    window.addEventListener("beforeinstallprompt", this.handleBeforeInstallPrompt);
    window.addEventListener("appinstalled", this.handleInstalled);
    window.addEventListener("pageshow", this.refreshInstalled);
    document.addEventListener("visibilitychange", this.refreshInstalled);
    for (const query of this.displayModeQueries()) {
      query.addEventListener?.("change", this.refreshInstalled);
    }

    // 事件迟迟不来（iOS Safari / Chrome 启发式压住）也要落定，否则按钮永远不出现。
    this.probeTimer = setTimeout(() => {
      this.probeTimer = null;
      if (!this.state.probed) {
        this.patch({ probed: true });
      }
    }, PROBE_TIMEOUT_MS);
  }

  private settleProbe(): void {
    if (this.probeTimer !== null) {
      clearTimeout(this.probeTimer);
      this.probeTimer = null;
    }
  }

  /**
   * 这个浏览器会不会派发 beforeinstallprompt。
   *
   * 用来决定「判定中」要不要设门。Safari（含 iOS/macOS）从不派发该事件——它的安装
   * 入口是分享菜单，不走 beforeinstallprompt；对它设门等于白等一个不会来的事件，
   * 只会把按钮推迟到 PROBE_TIMEOUT_MS 才出现。
   *
   * 这里用 Safari 的引擎特征而不是 UA 品牌枚举：`navigator.standalone` 只有
   * Safari 有；`AppleWebKit` + Safari 特征 token（不带 CriOS/FxiOS/Edg 等 Chromium
   * 系后缀）用来区分桌面 Safari 与 iOS 上伪装成 Safari 的第三方浏览器。UA 枚举会
   * 随新浏览器出现而失效，这两条是引擎级的。
   */
  expectsPrompt(): boolean {
    if (typeof navigator === "undefined") {
      return false;
    }
    const nav = navigator as Navigator & { standalone?: boolean };
    // iOS Safari 独有
    if (nav.standalone !== undefined) {
      return false;
    }
    const ua = nav.userAgent || "";
    const isWebKit = /AppleWebKit/i.test(ua);
    // 第三方 iOS 浏览器（CriOS/FxiOS/Edg…）底层是 Chromium，会派发该事件
    const isChromiumIosShell = /CriOS|FxiOS|EdgiOS|OPiOS|Chrome\//i.test(ua);
    if (isWebKit && /Safari\//i.test(ua) && !isChromiumIosShell) {
      return false;
    }
    return true;
  }

  private displayModeQueries(): MediaQueryList[] {
    if (typeof window === "undefined") {
      return [];
    }
    return [
      window.matchMedia("(display-mode: standalone)"),
      window.matchMedia("(display-mode: window-controls-overlay)"),
      window.matchMedia("(display-mode: fullscreen)"),
    ];
  }

  private patch(partial: Partial<PwaInstallSnapshot>): void {
    this.state = { ...this.state, ...partial };
    this.listeners.forEach((listener) => listener(this.snapshot()));
  }

  subscribe(listener: PwaInstallListener): () => void {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => {
      this.listeners.delete(listener);
    };
  }

  snapshot(): PwaInstallSnapshot {
    return this.state;
  }

  /** 消费掉这次性的原生 event：它只能用一次，且用户关掉弹窗后就不该再指望它。 */
  consumeDeferredPrompt(): BeforeInstallPromptEvent | null {
    const prompt = this.state.deferredPrompt;
    if (prompt) {
      this.patch({ deferredPrompt: null, probed: true });
    }
    return prompt;
  }

  markInstalled(): void {
    persistInstallState();
    this.patch({ installed: true, deferredPrompt: null, probed: true });
  }
}

export const pwaInstallService = new PwaInstallService();
