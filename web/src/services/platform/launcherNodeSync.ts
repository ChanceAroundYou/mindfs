import { registerPlugin } from "@capacitor/core";
import { getNativeBridge, parseNativeJSON } from "./nativeBridge";
import { isNativeShellRuntime, supportsCustomNativePlugins } from "./runtime";
import type { LauncherNode } from "../prefs/storage";

type LauncherNodeSyncPlugin = {
  getLauncherNodes: () => Promise<{ nodes?: LauncherNode[]; count?: number }>;
  setLauncherNodes: (input: {
    nodes: LauncherNode[];
  }) => Promise<{ stored?: boolean; count?: number }>;
};

const LauncherNodeSync = registerPlugin<LauncherNodeSyncPlugin>(
  "LauncherNodeSync",
);

function normalizeNodesResult<T>(result: unknown): T[] {
  const parsed = parseNativeJSON<unknown>(result, []);
  if (Array.isArray(parsed)) {
    return parsed as T[];
  }
  if (parsed && typeof parsed === "object" && Array.isArray((parsed as { nodes?: unknown[] }).nodes)) {
    return (parsed as { nodes: T[] }).nodes;
  }
  return [];
}

export async function getNativeLauncherNodes(): Promise<LauncherNode[]> {
  if (!isNativeShellRuntime()) {
    return [];
  }
  try {
    const native = getNativeBridge();
    if (typeof native?.getLauncherNodes === "function") {
      return normalizeNodesResult<LauncherNode>(await native.getLauncherNodes());
    }
    if (!supportsCustomNativePlugins()) {
      // iOS 壳没有原生节点表（iOS 侧不写自定义 Swift 插件）：节点存在登录页那份 Web 存储里，
      // WKWebView 的 localStorage 是持久的，不需要第二份真相。见 docs/ios-shell.md。
      return [];
    }
    const result = await LauncherNodeSync.getLauncherNodes();
    return Array.isArray(result?.nodes) ? result.nodes : [];
  } catch (error) {
    console.warn("[launcher-node-sync] restore failed", error);
    return [];
  }
}

export async function setNativeLauncherNodes(nodes: LauncherNode[]): Promise<void> {
  if (!isNativeShellRuntime()) {
    return;
  }
  try {
    const native = getNativeBridge();
    if (typeof native?.setLauncherNodes === "function") {
      await native.setLauncherNodes(JSON.stringify({ nodes }));
      return;
    }
    await LauncherNodeSync.setLauncherNodes({ nodes });
  } catch (error) {
    console.warn("[launcher-node-sync] persist failed", error);
  }
}
