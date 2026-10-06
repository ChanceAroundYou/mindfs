/**
 * 自动重载的观测（`Scope: G-AO`）—— **只记录，不干预**。
 *
 * 「标签页崩溃 / 自动重载」是最初报告的主要故障形态。前几轮优化打的是它的**假设**根因
 * （请求风暴、无界缓存、超线性渲染），但一直没有一条直接证据说明重载本身来自哪里。
 * 已经排除的只有 `staleAssetRecovery` 成环（它按 sessionStorage 保证同一资源只重载一次）。
 * 剩下的候选只能靠现场数据区分：
 *
 *   - `navType=reload` 且 loads 快速累加 → 有代码在主动 reload，去查调用点
 *   - `navType=navigate` 且 peakHeapMB 逼近几百 MB → 标签页被 OOM 杀掉后用户重新进入
 *   - 单条、loads=1 → 正常打开，什么都没发生
 *
 * 记录写在 sessionStorage（跨重载存活、关标签页即清），最近 10 条封顶 ——
 * 观测器本身绝不能变成新的无界增长源（那是 G-AH 修过的问题）。
 *
 * 读法：控制台 `__mindfsReloadReport()`。
 */

const STORAGE_KEY = "mindfs.reloadObserver";
const SAMPLE_LIMIT = 10;
const ERROR_LIMIT = 200;
const HEAP_SAMPLE_MS = 30_000;

export type ReloadSample = {
  at: number;
  navType: string;
  loads: number;
  peakHeapMB: number | null;
  lastError: string | null;
};

export type ReloadReport = {
  loads: number;
  lastUnloadAt: number | null;
  samples: ReloadSample[];
};

/** 只取这个模块真正用到的那几个成员 —— 便于用假对象测试。 */
type ObserverWindow = {
  sessionStorage?: Pick<Storage, "getItem" | "setItem">;
  performance?: { getEntriesByType?: (type: string) => Array<{ type?: string }>; memory?: { usedJSHeapSize?: number } };
  addEventListener?: (type: string, handler: (event?: unknown) => void) => void;
};

function sessionStorageOf(win: ObserverWindow): Pick<Storage, "getItem" | "setItem"> | null {
  try {
    return win.sessionStorage ?? null;
  } catch {
    // 隐私模式 / 存储被禁用时访问会抛
    return null;
  }
}

export function readReloadReport(win: ObserverWindow = window as unknown as ObserverWindow): ReloadReport | null {
  const storage = sessionStorageOf(win);
  if (!storage) {
    return null;
  }
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<ReloadReport>;
    if (!parsed || !Array.isArray(parsed.samples)) {
      return null;
    }
    return {
      loads: typeof parsed.loads === "number" ? parsed.loads : parsed.samples.length,
      lastUnloadAt: typeof parsed.lastUnloadAt === "number" ? parsed.lastUnloadAt : null,
      samples: parsed.samples.slice(-SAMPLE_LIMIT),
    };
  } catch {
    // 存储里是别的什么，或者被写坏了 —— 观测器不能因此影响启动
    return null;
  }
}

function writeReloadReport(win: ObserverWindow, report: ReloadReport): void {
  const storage = sessionStorageOf(win);
  if (!storage) {
    return;
  }
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify(report));
  } catch {
    // 配额满/隐私模式：丢掉这次采样即可
  }
}

/** 本次加载的类型：reload / navigate / back_forward / prerender（拿不到就空串）。 */
function navigationType(win: ObserverWindow): string {
  try {
    const entry = win.performance?.getEntriesByType?.("navigation")?.[0];
    return String(entry?.type || "");
  } catch {
    return "";
  }
}

function usedHeapMB(win: ObserverWindow): number | null {
  try {
    const used = win.performance?.memory?.usedJSHeapSize;
    if (typeof used !== "number" || !Number.isFinite(used)) {
      return null;
    }
    return Math.round(used / (1024 * 1024));
  } catch {
    return null;
  }
}

export function installReloadObserver(win: ObserverWindow = window as unknown as ObserverWindow): () => void {
  const previous = readReloadReport(win);
  const previousPeak = previous?.samples[previous.samples.length - 1]?.peakHeapMB ?? null;

  const report: ReloadReport = {
    loads: (previous?.loads ?? 0) + 1,
    lastUnloadAt: previous?.lastUnloadAt ?? null,
    samples: [
      ...(previous?.samples ?? []),
      { at: Date.now(), navType: navigationType(win), loads: (previous?.loads ?? 0) + 1, peakHeapMB: previousPeak, lastError: null },
    ].slice(-SAMPLE_LIMIT),
  };
  writeReloadReport(win, report);

  const current = () => report.samples[report.samples.length - 1];
  const flush = () => writeReloadReport(win, report);

  // 内存峰值：只在 Chromium 有 performance.memory，其它浏览器这段是空转。
  const sampleHeap = () => {
    const mb = usedHeapMB(win);
    if (mb === null) {
      return;
    }
    const entry = current();
    entry.peakHeapMB = Math.max(entry.peakHeapMB ?? 0, mb);
    flush();
  };
  sampleHeap();
  // 观测器不该拖住页面卸载，用 setInterval 而不是 rAF/长任务。
  const heapTimer = setInterval(sampleHeap, HEAP_SAMPLE_MS);

  win.addEventListener?.("beforeunload", () => {
    report.lastUnloadAt = Date.now();
    sampleHeap();
    flush();
  });
  win.addEventListener?.("error", (event) => {
    markLastError(win, report, "error", event);
  });
  win.addEventListener?.("unhandledrejection", (event) => {
    markLastError(win, report, "unhandledrejection", event);
  });

  (win as { __mindfsReloadReport?: () => ReloadReport }).__mindfsReloadReport = () => readReloadReport(win) ?? report;
  return () => clearInterval(heapTimer);
}

/**
 * 记一条「本轮加载里见到的第一个错误」。这里刻意**不**保存 error 对象或堆栈 ——
 * 只留一行截断文本，够区分「崩在渲染」与「崩在别处」，又不会把 sessionStorage 撑满。
 */
function markLastError(win: ObserverWindow, report: ReloadReport, kind: string, event: unknown): void {
  const entry = report.samples[report.samples.length - 1];
  if (!entry || entry.lastError) {
    return;
  }
  entry.lastError = `${kind}: ${errorSummary(event)}`.slice(0, ERROR_LIMIT);
  writeReloadReport(win, report);
}

function errorSummary(event: unknown): string {
  const source = event as { message?: unknown; reason?: unknown; error?: unknown } | null | undefined;
  const raw = source?.message ?? source?.reason ?? source?.error ?? "";
  const text = raw instanceof Error ? `${raw.name}: ${raw.message}` : String(raw);
  return text.replace(/\s+/g, " ").trim();
}
