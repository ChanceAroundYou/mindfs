import { registerPlugin } from "@capacitor/core";
import { getNativeBridge, parseNativeJSON } from "./nativeBridge";
import { isHarmonyRuntime, isNativeShellRuntime } from "./runtime";
import type { LauncherNode } from "./storage";

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
    if (isHarmonyRuntime()) {
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
    if (isHarmonyRuntime()) {
      return;
    }
    await LauncherNodeSync.setLauncherNodes({ nodes });
  } catch (error) {
    console.warn("[launcher-node-sync] persist failed", error);
  }
}
