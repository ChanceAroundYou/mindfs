import type { LauncherNode } from "../../services/prefs/storage";
import type { AppUpdateState } from "../../services/appUpdate";
import { appPackageLabel } from "../../services/appUpdate";
import type { MessageKey, MessageParams } from "../../i18n/index";

export function normalizeNodeURL(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    return "";
  }
  const withScheme = /^[a-z]+:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
  try {
    const parsed = new URL(withScheme);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return "";
    }
    parsed.hash = "";
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return "";
  }
}

export function sortNodes(nodes: LauncherNode[]): LauncherNode[] {
  return [...nodes].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export function buildNodeID(): string {
  return `node-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function normalizeLauncherNodes(nodes: LauncherNode[]): LauncherNode[] {
  return nodes
    .map((item) => {
      const id = String(item?.id || "").trim();
      const name = String(item?.name || "").trim();
      const url = normalizeNodeURL(String(item?.url || ""));
      const createdAt = String(item?.createdAt || "").trim();
      const lastOpenedAt = String(item?.lastOpenedAt || "").trim();
      if (!id || !name || !url || !createdAt) {
        return null;
      }
      return {
        id,
        name,
        url,
        createdAt,
        ...(lastOpenedAt ? { lastOpenedAt } : {}),
      };
    })
    .filter((item): item is LauncherNode => item !== null);
}

export function mergeLauncherNodes(...groups: LauncherNode[][]): LauncherNode[] {
  const seenURLs = new Set<string>();
  const merged: LauncherNode[] = [];
  for (const group of groups) {
    for (const node of normalizeLauncherNodes(group)) {
      if (seenURLs.has(node.url)) {
        continue;
      }
      seenURLs.add(node.url);
      merged.push(node);
    }
  }
  return sortNodes(merged);
}

export function shouldShowAppUpdate(state: AppUpdateState): boolean {
  const status = (state.status || "idle").toLowerCase();
  return (
    state.has_update === true ||
    status === "downloading" ||
    status === "downloaded" ||
    status === "failed"
  );
}

export function appUpdateSummary(
  state: AppUpdateState,
  t: (key: MessageKey, params?: MessageParams) => string,
): string {
  const notes = String(state.notes || "").trim();
  if (notes) {
    return notes;
  }
  if (state.latest_version) {
    return t("login.updateAvailable", {
      packageLabel: appPackageLabel(state.platform),
      version: state.latest_version,
    });
  }
  return "";
}
