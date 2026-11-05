// Android 应用自更新：直接比对 GitHub Release 的 APK 版本号，有更新则下载并拉起安装器。
// 只服务原生 APK 通道；服务端驱动的 web/桌面更新仍在 appUpdate.ts。
const RELEASES_URL = "https://api.github.com/repos/ChanceAroundYou/mindfs/releases?per_page=20";

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

export async function checkApkUpdate(): Promise<ApkUpdateCheck> {
  const current = currentApkVersionCode();
  if (!current) {
    return { kind: "unknown", reason: "读取当前版本号失败" };
  }
  const response = await fetch(RELEASES_URL, {
    cache: "no-cache",
    headers: { Accept: "application/vnd.github+json" },
  });
  if (!response.ok) {
    throw new Error(`GitHub 返回 ${response.status}`);
  }
  const releases = (await response.json()) as GitHubRelease[];
  const candidates = (Array.isArray(releases) ? releases : [])
    .filter((item) => !item.draft && !item.prerelease)
    .map(releaseFromJSON)
    .filter((item): item is ApkRelease => item !== null)
    .filter((item) => item.versionCode > current)
    .sort((left, right) => right.versionCode - left.versionCode);
  if (!candidates.length) {
    return { kind: "latest", tag: releases?.[0]?.tag_name || "" };
  }
  return { kind: "available", release: candidates[0] };
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
