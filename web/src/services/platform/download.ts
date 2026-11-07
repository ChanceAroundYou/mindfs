import { registerPlugin } from "@capacitor/core";
import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { getNativeBridge } from "./nativeBridge";
import { getApiBaseURL, isNativeShellRuntime, supportsCustomNativePlugins } from "./runtime";
import { translateNow } from "../../i18n/index";
import type { MessageKey } from "../../i18n/types";

type DownloadFileParams = {
  rootId: string;
  path: string;
  name?: string;
  nodeId?: string;
};

// 下载落在哪儿：由各条路径回报给用户看的文案 key（iOS 与安卓的落盘位置不同，不能共用一句）。
export type DownloadResult = { savedLabelKey?: MessageKey };

type NativeDownloadPlugin = {
  download: (opts: { url: string; filename: string }) => Promise<{
    downloadId: number;
    filename: string;
    directory: string;
  }>;
  saveBase64: (opts: { dataBase64: string; filename: string; mimeType?: string }) => Promise<{
    filename: string;
    directory: string;
    path?: string;
  }>;
};

const NativeDownload = registerPlugin<NativeDownloadPlugin>("NativeDownload");

type WindowWithNativeDownloadBridge = Window & {
  MindFSNativeDownload?: {
    download?: (url: string, filename: string) => string;
    saveBase64?: (dataBase64: string, filename: string, mimeType?: string) => string;
  };
};

function sanitizeDownloadName(path: string, name?: string): string {
  const candidate = String(name || path || "").trim();
  if (!candidate) {
    return "download";
  }
  const parts = candidate.replace(/\\/g, "/").split("/").filter(Boolean);
  return parts[parts.length - 1] || "download";
}

function buildDownloadURL(rootId: string, path: string, nodeId?: string): string {
  return appURL("/api/file", new URLSearchParams({
    raw: "1",
    root: rootId,
    path,
    download: "1",
  }), nodeId);
}

function toAbsoluteDownloadURL(url: string, nodeId?: string): string {
  if (/^https?:\/\//i.test(url)) {
    return url;
  }

  const apiBaseURL = getApiBaseURL(nodeId);
  if (apiBaseURL) {
    return new URL(url, `${apiBaseURL.replace(/\/+$/, "")}/`).toString();
  }

  if (typeof window !== "undefined" && /^https?:$/i.test(window.location.protocol)) {
    return new URL(url, window.location.href).toString();
  }

  return url;
}

function triggerBrowserDownload(url: string, filename: string): void {
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.rel = "noopener";
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

// iOS 壳的落盘位置：<App 沙箱>/Documents/MindFS/<文件名>，在「文件」App 的「我的 iPad」下可见。
const IOS_DOWNLOAD_DIR = "MindFS/";

function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const raw = String(reader.result || "");
      // readAsDataURL 给的是 data:<mime>;base64,<...>；Filesystem 只吃纯 base64。
      resolve(raw.includes(",") ? raw.slice(raw.indexOf(",") + 1) : raw);
    };
    reader.onerror = () => reject(new Error(translateNow("download.readFailed")));
    reader.readAsDataURL(blob);
  });
}

async function fetchAsBase64(url: string): Promise<string> {
  let response: Response;
  try {
    response = await fetch(url, { credentials: "include", cache: "no-store" });
  } catch {
    throw new Error(translateNow("download.readFailed"));
  }
  if (!response.ok) {
    throw new Error(translateNow("download.readFailed"));
  }
  return blobToBase64(await response.blob());
}

// iOS 壳没有 NativeDownload 插件（iOS 侧刻意不写自定义 Swift 插件，见 docs/ios-shell.md），
// 而 WKWebView 里 <a download> 根本不起作用 —— 所以走官方 @capacitor/filesystem 写进 App 的沙箱目录。
async function downloadWithIOSShell(url: string, filename: string): Promise<DownloadResult> {
  const dataBase64 = await fetchAsBase64(url);
  const { Filesystem, Directory } = await import("@capacitor/filesystem");
  await Filesystem.writeFile({
    path: `${IOS_DOWNLOAD_DIR}${filename}`,
    data: dataBase64,
    directory: Directory.Documents,
    recursive: true,
  });
  return { savedLabelKey: "fileViewer.downloadSavedIOSFiles" };
}

async function downloadWithNativeShell(url: string, filename: string): Promise<DownloadResult> {
  if (!/^https?:\/\//i.test(url)) {
    throw new Error(translateNow("download.absoluteURLRequired"));
  }

  const unifiedBridge = getNativeBridge();
  if (typeof unifiedBridge?.download === "function") {
    const result = await unifiedBridge.download(JSON.stringify({ url, filename }));
    if (typeof result === "string" && result) {
      throw new Error(result);
    }
    return { savedLabelKey: "fileViewer.downloadSavedNative" };
  }

  const nativeBridge = (window as WindowWithNativeDownloadBridge).MindFSNativeDownload;
  if (nativeBridge && typeof nativeBridge.download === "function") {
    const errorMessage = nativeBridge.download(url, filename);
    if (errorMessage) {
      throw new Error(errorMessage);
    }
    return {};
  }

  if (!supportsCustomNativePlugins()) {
    return downloadWithIOSShell(url, filename);
  }

  await NativeDownload.download({ url, filename });
  return { savedLabelKey: "fileViewer.downloadSavedNative" };
}

export async function downloadURL(url: string, filename = "download"): Promise<DownloadResult> {
  if (typeof document === "undefined") {
    throw new Error("download is only available in browser runtime");
  }

  const safeFilename = sanitizeDownloadName(filename, filename);
  const absoluteURL = toAbsoluteDownloadURL(url);
  if (isNativeShellRuntime()) {
    return downloadWithNativeShell(absoluteURL, safeFilename);
  }

  triggerBrowserDownload(absoluteURL, safeFilename);
  return {};
}

export async function downloadFile(params: DownloadFileParams & { nodeId?: string }): Promise<DownloadResult> {
  params.nodeId = params.nodeId || getRootNodeId(params.rootId);
  const filename = sanitizeDownloadName(params.path, params.name);
  const url = toAbsoluteDownloadURL(buildDownloadURL(params.rootId, params.path, params.nodeId), params.nodeId);
  return downloadURL(url, filename);
}
