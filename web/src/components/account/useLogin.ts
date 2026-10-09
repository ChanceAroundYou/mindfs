import { useEffect, useState } from "react";
import {
  getStoredLauncherNodes,
  setStoredLauncherNodes,
  type LauncherNode,
} from "../../services/prefs/storage";
import {
  getNativeLauncherNodes,
  setNativeLauncherNodes,
} from "../../services/platform/launcherNodeSync";
import {
  appPackageLabel,
  fetchAppUpdateState,
  isUpdatableNativeRuntime,
  normalizeAppUpdateState,
  type AppUpdateState,
} from "../../services/appUpdate";
import { downloadURL } from "../../services/platform/download";
import { useI18n } from "../../i18n/index";
import {
  normalizeNodeURL,
  sortNodes,
  buildNodeID,
  normalizeLauncherNodes,
  mergeLauncherNodes,
  shouldShowAppUpdate,
  appUpdateSummary,
} from "./loginUtils";

export function useLogin({ onOpenNode }: { onOpenNode: (nodeURL: string) => void }) {
  const { t } = useI18n();
  const [nodes, setNodes] = useState<LauncherNode[]>(() => sortNodes(getStoredLauncherNodes()));
  const [composerOpen, setComposerOpen] = useState(false);
  const [nodeName, setNodeName] = useState("");
  const [nodeURL, setNodeURL] = useState("");
  const [formError, setFormError] = useState("");
  const [editingNodeID, setEditingNodeID] = useState("");
  const [editingNodeName, setEditingNodeName] = useState("");
  const [appUpdateState, setAppUpdateState] =
    useState<AppUpdateState>(() => normalizeAppUpdateState(null));
  const [appUpdateNotesOpen, setAppUpdateNotesOpen] = useState(false);
  const [appUpdateBusy, setAppUpdateBusy] = useState(false);

  function persistNodes(nextNodes: LauncherNode[]): void {
    const sorted = sortNodes(nextNodes);
    setNodes(sorted);
    setStoredLauncherNodes(sorted);
    void setNativeLauncherNodes(sorted);
  }

  function openNode(node: LauncherNode): void {
    onOpenNode(node.url);
  }

  function handleDeleteNode(nodeID: string): void {
    if (editingNodeID === nodeID) {
      setEditingNodeID("");
      setEditingNodeName("");
    }
    persistNodes(nodes.filter((item) => item.id !== nodeID));
  }

  function handleStartRename(node: LauncherNode): void {
    setEditingNodeID(node.id);
    setEditingNodeName(node.name);
  }

  function handleCancelRename(): void {
    setEditingNodeID("");
    setEditingNodeName("");
  }

  function handleCommitRename(node: LauncherNode): void {
    const trimmedName = editingNodeName.trim();
    if (!trimmedName) {
      setEditingNodeName(node.name);
      setEditingNodeID("");
      return;
    }
    if (trimmedName === node.name) {
      setEditingNodeID("");
      setEditingNodeName("");
      return;
    }
    persistNodes(
      nodes.map((item) =>
        item.id === node.id ? { ...item, name: trimmedName } : item
      )
    );
    setEditingNodeID("");
    setEditingNodeName("");
  }

  function handleSaveNode(event: React.FormEvent): void {
    event.preventDefault();
    const trimmedName = nodeName.trim();
    const normalizedURL = normalizeNodeURL(nodeURL);
    if (!trimmedName) {
      setFormError("Node name is required.");
      return;
    }
    if (!normalizedURL) {
      setFormError("Enter a valid http:// or https:// node URL.");
      return;
    }
    if (nodes.some((item) => item.url === normalizedURL)) {
      setFormError("This node URL already exists.");
      return;
    }
    const createdAt = new Date().toISOString();
    const nextNode: LauncherNode = {
      id: buildNodeID(),
      name: trimmedName,
      url: normalizedURL,
      createdAt,
    };
    persistNodes([nextNode, ...nodes]);
    setNodeName("");
    setNodeURL("");
    setFormError("");
    setComposerOpen(false);
  }

  async function handleDownloadAppUpdate(): Promise<void> {
    const next = normalizeAppUpdateState(appUpdateState);
    const packageLabel = appPackageLabel(next.platform);
    if (!next.download_url || appUpdateBusy) {
      return;
    }
    setAppUpdateBusy(true);
    setAppUpdateState((prev) =>
      normalizeAppUpdateState({
        ...prev,
        status: "downloading",
        message: t("login.downloadingPackage", { packageLabel }),
      }),
    );
    try {
      await downloadURL(next.download_url, next.filename || "");
      setAppUpdateState((prev) =>
        normalizeAppUpdateState({
          ...prev,
          status: "downloaded",
          message: t("login.packageDownloadStarted"),
        }),
      );
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("login.packageDownloadFailed", { packageLabel });
      setAppUpdateState((prev) =>
        normalizeAppUpdateState({
          ...prev,
          status: "failed",
          message,
        }),
      );
    } finally {
      setAppUpdateBusy(false);
    }
  }

  useEffect(() => {
    let cancelled = false;
    const timers: number[] = [];

    const syncLauncherNodes = async (): Promise<void> => {
      const nativeNodes = await getNativeLauncherNodes();
      if (cancelled) {
        return;
      }

      const existingNodes = getStoredLauncherNodes();
      const nextNodes = mergeLauncherNodes(existingNodes, nativeNodes);
      if (nextNodes.length === existingNodes.length) {
        return;
      }

      setStoredLauncherNodes(nextNodes);
      void setNativeLauncherNodes(nextNodes);
      if (!cancelled) {
        setNodes(nextNodes);
      }
    };

    const handleLauncherNodesUpdated = (): void => {
      void syncLauncherNodes();
    };

    void syncLauncherNodes();
    timers.push(window.setTimeout(() => void syncLauncherNodes(), 1200));
    timers.push(window.setTimeout(() => void syncLauncherNodes(), 3200));
    window.addEventListener("mindfs:launcher-nodes-updated", handleLauncherNodesUpdated);

    return () => {
      cancelled = true;
      window.removeEventListener("mindfs:launcher-nodes-updated", handleLauncherNodesUpdated);
      timers.forEach((timer) => window.clearTimeout(timer));
    };
  }, []);

  useEffect(() => {
    if (!isUpdatableNativeRuntime() || nodes.length === 0) {
      setAppUpdateState(normalizeAppUpdateState(null));
      return;
    }

    let cancelled = false;
    void fetchAppUpdateState()
      .then((state) => {
        if (!cancelled) {
          setAppUpdateState(state);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          console.warn("[app-update] launcher check failed", error);
          setAppUpdateState(normalizeAppUpdateState(null));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [nodes.length]);

  const showAppUpdate = shouldShowAppUpdate(appUpdateState);
  const appUpdateStatus = (appUpdateState.status || "idle").toLowerCase();
  const appUpdateDisabled =
    appUpdateBusy ||
    appUpdateStatus === "downloading" ||
    appUpdateStatus === "downloaded";
  const appUpdateText =
    appUpdateStatus === "downloading"
      ? t("login.updateDownloading")
      : appUpdateStatus === "downloaded"
        ? t("login.updateDownloaded")
        : t("login.updateApp");
  const appUpdateHelp =
    appUpdateState.message ||
    (appUpdateState.latest_version
      ? t("login.updateVersionHelp", {
          current: appUpdateState.current_version || t("login.unknownVersion"),
          latest: appUpdateState.latest_version,
        })
      : "");
  const appUpdateNotes = appUpdateSummary(appUpdateState, t);

  return {
    t,
    nodes,
    composerOpen,
    setComposerOpen,
    nodeName,
    setNodeName,
    nodeURL,
    setNodeURL,
    formError,
    setFormError,
    editingNodeID,
    setEditingNodeID,
    editingNodeName,
    setEditingNodeName,
    appUpdateState,
    setAppUpdateState,
    appUpdateNotesOpen,
    setAppUpdateNotesOpen,
    appUpdateBusy,
    persistNodes,
    openNode,
    handleDeleteNode,
    handleStartRename,
    handleCancelRename,
    handleCommitRename,
    handleSaveNode,
    handleDownloadAppUpdate,
    showAppUpdate,
    appUpdateStatus,
    appUpdateDisabled,
    appUpdateText,
    appUpdateHelp,
    appUpdateNotes,
  };
}
