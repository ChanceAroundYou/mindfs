import { registerPlugin } from "@capacitor/core";
import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { getNativeBridge } from "./nativeBridge";
import { getApiBaseURL, isNativeShellRuntime } from "./runtime";
import { translateNow } from "../../i18n/index";

type DownloadFileParams = {
  rootId: string;
  path: string;
  name?: string;
  nodeId?: string;
};

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

async function downloadWithNativeShell(url: string, filename: string): Promise<void> {
  if (!/^https?:\/\//i.test(url)) {
    throw new Error(translateNow("download.absoluteURLRequired"));
  }

  const unifiedBridge = getNativeBridge();
  if (typeof unifiedBridge?.download === "function") {
    const result = await unifiedBridge.download(JSON.stringify({ url, filename }));
    if (typeof result === "string" && result) {
      throw new Error(result);
    }
    return;
  }

  const nativeBridge = (window as WindowWithNativeDownloadBridge).MindFSNativeDownload;
  if (nativeBridge && typeof nativeBridge.download === "function") {
    const errorMessage = nativeBridge.download(url, filename);
    if (errorMessage) {
      throw new Error(errorMessage);
    }
    return;
  }

  await NativeDownload.download({ url, filename });
}

export async function downloadURL(url: string, filename = "download"): Promise<void> {
  if (typeof document === "undefined") {
    throw new Error("download is only available in browser runtime");
  }

  const safeFilename = sanitizeDownloadName(filename, filename);
  const absoluteURL = toAbsoluteDownloadURL(url);
  if (isNativeShellRuntime()) {
    await downloadWithNativeShell(absoluteURL, safeFilename);
    return;
  }

  triggerBrowserDownload(absoluteURL, safeFilename);
}

export async function downloadFile(params: DownloadFileParams & { nodeId?: string }): Promise<void> {
  params.nodeId = params.nodeId || getRootNodeId(params.rootId);
  const filename = sanitizeDownloadName(params.path, params.name);
  const url = toAbsoluteDownloadURL(buildDownloadURL(params.rootId, params.path, params.nodeId), params.nodeId);
  await downloadURL(url, filename);
}
