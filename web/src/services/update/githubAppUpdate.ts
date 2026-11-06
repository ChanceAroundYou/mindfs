// Android 应用自更新：比对发布清单里的 APK 版本号，有更新则下载并拉起安装器。
// 只服务原生 APK 通道；服务端驱动的 web/桌面更新仍在 appUpdate.ts。
//
// ── 为什么不再用 GitHub REST API（2026-11-05 实测） ──────────────────────
// 旧实现直接 fetch 未认证的 api.github.com/repos/.../releases。未认证限流是
// 60 次/小时且【按出口 IP 计】，而手机在内网走的是机场出口，同一落地机 IP 上
// 挂着大量他人的 GitHub API 流量，配额常年为 0：
//     走机场出口(52.199.72.174)：403 "API rate limit exceeded for 52.199.72.174"
//     自家出口                  ：200，core 57/60
// GitHub 节点是 fallback 组、16 个成员轮换，于是时而选到被用光的 IP 就 403，
// 表现为「检查更新时好时坏」。本文件改为读发布清单，主路径不碰 api.github.com。
//
// ── 为什么是 android-version.json ─────────────────────────────────────────
// make android-version-json（scripts/android-version-manifest.go）每次发布都会
// 随 APK 一起上传 android-version.json，含 version / notes / downloads[].url、
// filename、size。它挂在 releases/latest/download/ 下，是【不随版本变化的固定
// URL】，无需知道 tag 就能取到最新一版。
// 该路径走 github.com → 302 → release-assets.githubusercontent.com，属于
// 普通发布资源下载，不受 REST API 的 60/h 限制（APK 本体就在同一路径下）。
//
// 注意：清单响应没有 Access-Control-Allow-Origin，所以浏览器 fetch 会被 CORS
// 拦掉 —— 原生壳里必须走 CapacitorHttp（与 appUpdate.ts 同一做法）。
// REST API 兜底保留，但仅用其公开字段；它失败不影响主路径。

const MANIFEST_URL =
  "https://github.com/ChanceAroundYou/mindfs/releases/latest/download/android-version.json";
const RELEASES_API_URL =
  "https://api.github.com/repos/ChanceAroundYou/mindfs/releases?per_page=20";

type ManifestDownload = { os?: string; arch?: string; filename?: string; url?: string; size?: number };
type ReleaseManifest = { version?: string; notes?: string; downloads?: ManifestDownload[] };

type GitHubAsset = { name?: string; size?: number; browser_download_url?: string };
type GitHubRelease = {
  tag_name?: string;
  body?: string;
  draft?: boolean;
  prerelease?: boolean;
  assets?: GitHubAsset[];
};

export type ApkRelease = {
  tag: string;
  notes: string;
  downloadUrl: string;
  filename: string;
  versionCode: number;
};

export type ApkUpdateCheck =
  | { kind: "latest"; tag: string }
  | { kind: "available"; release: ApkRelease }
  | { kind: "unknown"; reason: string };

type WindowWithApkBridge = Window & {
  MindFSAppInfo?: { getInfo?: () => string };
  MindFSNativeDownload?: {
    downloadAndInstallApk?: (url: string, filename: string) => string;
  };
};

// 与 web/android/app/build.gradle 的 mindfsVersionCodeFromName 同一条公式，
// 改一边必须改另一边：APK 的 android:versionCode 就是按它算的。
export function apkVersionCodeFromTag(tag: string): number {
  const match = String(tag || "").trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+))?/);
  if (!match) return 0;
  const [major, minor, patch] = [match[1], match[2], match[3]].map((part) => Number.parseInt(part, 10));
  const build = match[4] ? Number.parseInt(match[4], 10) : 0;
  return Math.max(1, major * 10000000 + minor * 100000 + patch * 1000 + build + 1);
}

export function currentApkVersionCode(): number {
  if (typeof window === "undefined") return 0;
  try {
    const raw = (window as WindowWithApkBridge).MindFSAppInfo?.getInfo?.();
    const build = raw ? Number.parseInt(String(JSON.parse(raw).build || ""), 10) : 0;
    return Number.isFinite(build) ? build : 0;
  } catch {
    return 0;
  }
}

function releaseFromJSON(item: GitHubRelease): ApkRelease | null {
  const tag = String(item.tag_name || "").trim();
  const versionCode = apkVersionCodeFromTag(tag);
  const apk = (item.assets || []).find((asset) => /\.apk$/i.test(asset.name || ""));
  if (!tag || !versionCode || !apk?.browser_download_url) return null;
  return {
    tag,
    notes: String(item.body || ""),
    downloadUrl: apk.browser_download_url,
    filename: apk.name || "mindfs-update.apk",
    versionCode,
  };
}

// 清单里的 downloads 可能不止一项（将来加 arch 变体），优先挑安卓的 .apk。
export function releaseFromManifest(manifest: ReleaseManifest): ApkRelease | null {
  const tag = String(manifest?.version || "").trim();
  const versionCode = apkVersionCodeFromTag(tag);
  if (!tag || !versionCode) return null;
  const downloads = Array.isArray(manifest?.downloads) ? manifest.downloads : [];
  // 安卓包：os 缺省或为 android，且 url/文件名是 .apk。别把 harmony 的 .hap 认成 APK。
  const apk =
    downloads.find((item) => {
      const os = String(item?.os || "").toLowerCase();
      const looksApk =
        /\.apk(?:$|\?)/i.test(item?.url || "") || /\.apk$/i.test(item?.filename || "");
      return (!os || os === "android") && looksApk;
    }) || null;
  if (!apk?.url) return null;
  return {
    tag,
    notes: String(manifest?.notes || ""),
    downloadUrl: apk.url,
    filename: apk.filename || "mindfs-update.apk",
    versionCode,
  };
}

async function fetchJSON<T>(url: string, accept: string): Promise<T | null> {
  // 原生壳里 CapacitorHttp 走原生网络栈（无 CORS 约束）；浏览器环境退回 fetch。
  try {
    const { CapacitorHttp } = await import("@capacitor/core");
    const capacitor = (window as any)?.Capacitor;
    if (CapacitorHttp && typeof capacitor?.isNativePlatform === "function" && capacitor.isNativePlatform()) {
      const response = await CapacitorHttp.get({
        url,
        responseType: "json",
        headers: { Accept: accept, "Cache-Control": "no-cache" },
      });
      if (response.status >= 200 && response.status < 300) {
        const data = typeof response.data === "string" ? JSON.parse(response.data) : response.data;
        return (data ?? null) as T | null;
      }
      return null;
    }
  } catch {
    // 落到 fetch
  }
  const response = await fetch(url, { cache: "no-cache", headers: { Accept: accept } });
  if (!response.ok) return null;
  try {
    return (await response.json()) as T;
  } catch {
    return null;
  }
}

export async function checkApkUpdate(): Promise<ApkUpdateCheck> {
  const current = currentApkVersionCode();
  if (!current) {
    return { kind: "unknown", reason: "读取当前版本号失败" };
  }

  // 主路径：发布清单，不消耗 GitHub REST API 配额。
  let latest: ApkRelease | null = null;
  try {
    const manifest = await fetchJSON<ReleaseManifest>(MANIFEST_URL, "application/json");
    if (manifest) latest = releaseFromManifest(manifest);
  } catch {
    latest = null;
  }

  // 兜底：清单不可用时才问 REST API（有配额风险，但不至于让检查彻底失效）。
  if (!latest) {
    try {
      const releases = await fetchJSON<GitHubRelease[]>(RELEASES_API_URL, "application/vnd.github+json");
      const candidates = (Array.isArray(releases) ? releases : [])
        .filter((item) => !item.draft && !item.prerelease)
        .map(releaseFromJSON)
        .filter((item): item is ApkRelease => item !== null)
        .sort((left, right) => right.versionCode - left.versionCode);
      latest = candidates[0] || null;
    } catch {
      latest = null;
    }
  }

  if (!latest) {
    return { kind: "unknown", reason: "无法获取发布信息，请检查网络" };
  }
  if (latest.versionCode <= current) {
    return { kind: "latest", tag: latest.tag };
  }
  return { kind: "available", release: latest };
}

// 返回空串 = 已开始下载安装；非空 = 需要展示给用户的提示（如未授权安装未知应用）。
export function installApkUpdate(release: ApkRelease): string {
  if (typeof window === "undefined") return "仅支持在 App 内使用";
  const bridge = (window as WindowWithApkBridge).MindFSNativeDownload;
  if (typeof bridge?.downloadAndInstallApk !== "function") {
    return "当前 App 版本不支持自动安装，请到 Release 页手动下载";
  }
  try {
    return bridge.downloadAndInstallApk(release.downloadUrl, release.filename) || "";
  } catch (ex) {
    return ex instanceof Error ? ex.message : String(ex);
  }
}
