import { getNativeBridge, parseNativeJSON } from "../platform/nativeBridge";
import { isNewerVersion, normalizeVersionLabel } from "./appVersion";
import { getNativePlatform, isAndroidRuntime, isNativeShellRuntime, type NativePlatform } from "../platform/runtime";

export type AppUpdateState = {
  platform: NativePlatform;
  current_version: string;
  current_build: string;
  latest_version: string;
  has_update: boolean;
  status: "idle" | "available" | "downloading" | "downloaded" | "failed";
  message: string;
  notes: string;
  download_url: string;
  filename: string;
};

type AppVersionResponse = {
  version?: string;
  notes?: string;
  downloads?: Array<{
    os?: string;
    arch?: string;
    filename?: string;
    url?: string;
  }>;
};

type WindowWithAppInfoBridge = Window & {
  MindFSAppInfo?: {
    getInfo?: () => string;
  };
};

export function normalizeAppUpdateState(
  input: Partial<AppUpdateState> | null | undefined,
): AppUpdateState {
  return {
    platform: input?.platform || getNativePlatform(),
    current_version: input?.current_version || "",
    current_build: input?.current_build || "",
    latest_version: input?.latest_version || "",
    has_update: input?.has_update === true,
    status: input?.status || "idle",
    message: input?.message || "",
    notes: input?.notes || "",
    download_url: input?.download_url || "",
    filename: input?.filename || "",
  };
}

// 只有安卓壳能自己装更新包：APK = 下载 + 拉起系统安装器。
// iOS 没有这条通道 —— 装 ipa 必须重新签名（Xcode / Sideloadly），App 内部做不到，
// 所以 iOS 壳不显示「有新版本可装」，更新靠重新安装。
export function isUpdatableNativeRuntime(): boolean {
  return isAndroidRuntime();
}

export async function fetchAppUpdateState(): Promise<AppUpdateState> {
  if (!isUpdatableNativeRuntime()) {
    return normalizeAppUpdateState(null);
  }

  const platform = getNativePlatform();
  const endpoint = buildVersionEndpoint();
  if (!endpoint) {
    // 未配置版本检查地址：按「没有更新信息」处理，不拿空 URL 去 fetch。
    return normalizeAppUpdateState(null);
  }
  const appInfo = await getAppInfo();
  const { status, payload } = await fetchVersionPayload(endpoint, platform);
  if (status < 200 || status >= 300) {
    throw new Error(`${platform}_version_check_failed_${status}`);
  }

  const download = pickNativeDownload(payload, platform);
  const latestVersion = normalizeVersionLabel(payload.version || "");
  const currentVersion = normalizeVersionLabel(appInfo.version || "");
  const hasUpdate =
    !!latestVersion &&
    !!download.url &&
    isNewerVersion(latestVersion, currentVersion);

  return normalizeAppUpdateState({
    platform,
    current_version: appInfo.version || "",
    current_build: appInfo.build || "",
    latest_version: latestVersion,
    has_update: hasUpdate,
    status: hasUpdate ? "available" : "idle",
    notes: payload.notes || "",
    download_url: download.url || "",
    filename: download.filename || filenameFromURL(download.url || "") || defaultPackageFilename(),
  });
}

async function fetchVersionPayload(
  endpoint: string,
  platform: NativePlatform,
): Promise<{ status: number; payload: AppVersionResponse }> {
  if (isAndroidRuntime()) {
    try {
      const { CapacitorHttp } = await import("@capacitor/core");
      const response = await CapacitorHttp.get({
        url: endpoint,
        responseType: "json",
        headers: { Accept: "application/json", "Cache-Control": "no-cache" },
      });
      const payload =
        typeof response.data === "string" ? JSON.parse(response.data) : (response.data || {});
      return { status: response.status, payload };
    } catch {
      // fall through to fetch (will likely fail on CORS, but keeps behavior observable)
    }
  }
  const response = await fetch(endpoint, { cache: "no-cache" });
  let payload: AppVersionResponse = {};
  try {
    payload = (await response.json()) as AppVersionResponse;
  } catch {
    payload = {};
  }
  return { status: response.status, payload };
}

// 版本检查地址只由构建期环境变量提供，缺省为空 = 不做检查。
// 刻意不再内置上游 relay 域名（本 fork 已整体删除 relay，见 G-H）。
function buildVersionEndpoint(): string {
  return String(import.meta.env.VITE_ANDROID_VERSION_URL || "").trim();
}

export async function getAppInfo(): Promise<{ version?: string; build?: string }> {
  const native = getNativeBridge();
  if (typeof native?.getAppInfo === "function") {
    return parseNativeJSON(await native.getAppInfo(), {});
  }
  if (isNativeShellRuntime()) {
    const nativeBridge = (window as WindowWithAppInfoBridge).MindFSAppInfo;
    if (nativeBridge && typeof nativeBridge.getInfo === "function") {
      return parseNativeJSON(nativeBridge.getInfo(), {});
    }
    // 官方 @capacitor/app：版本取自 CFBundleShortVersionString / CURRENT_PROJECT_VERSION（iOS）
    // 与 versionName / versionCode（安卓），两个壳都认。它回报的字段叫 appVersion，不是 version。
    const { App } = await import("@capacitor/app");
    const info = await App.getInfo();
    return { version: info.version, build: String(info.build ?? "") };
  }
  return {};
}

function pickNativeDownload(payload: AppVersionResponse, platform: NativePlatform): {
  filename?: string;
  url?: string;
} {
  const downloads = Array.isArray(payload.downloads) ? payload.downloads : [];
  const platformDownloads = downloads.filter((item) => {
    const os = String(item.os || "").toLowerCase();
    return !os || os === platform;
  });
  const candidates = platformDownloads.length ? platformDownloads : downloads;
  const packagePattern = /\.apk(?:$|\?)/i;
  return (
    candidates.find((item) => packagePattern.test(item.url || item.filename || "")) ||
    candidates[0] ||
    {}
  );
}

function defaultPackageFilename(): string {
  return "mindfs-android.apk";
}

export function appPackageLabel(): string {
  return "Android";
}

function filenameFromURL(value: string): string {
  try {
    const url = new URL(value);
    const segment = url.pathname.split("/").filter(Boolean).pop() || "";
    return decodeURIComponent(segment);
  } catch {
    const segment = value.split("?")[0]?.split("/").filter(Boolean).pop() || "";
    try {
      return decodeURIComponent(segment);
    } catch {
      return segment;
    }
  }
}

export { isNativeShellRuntime };
export { isAndroidRuntime };
