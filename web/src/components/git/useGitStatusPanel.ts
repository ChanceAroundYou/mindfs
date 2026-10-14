import React, { useEffect, useRef, useState } from "react";
import { confirmDialog } from "../../services/dialog";
import { fetchGitBranches, type GitBranchItem } from "../../services/git";
import type { GitStatusItem } from "../../shared/gitTypes";
import type { MessageKey, MessageParams } from "../../i18n/index";

type UseGitStatusPanelOptions = {
  rootId?: string;
  onCommit?: (message: string) => void | Promise<void>;
  t: (key: MessageKey, options?: MessageParams) => string;
};

export function useGitStatusPanel({ rootId, onCommit, t }: UseGitStatusPanelOptions) {
  const branchMenuRef = useRef<HTMLDivElement | null>(null);
  const [branchMenuOpen, setBranchMenuOpen] = useState(false);
  const [branches, setBranches] = useState<GitBranchItem[]>([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [switchingBranch, setSwitchingBranch] = useState("");
  const [commitOpen, setCommitOpen] = useState(false);
  const [commitMessage, setCommitMessage] = useState("");
  const [actionBusy, setActionBusy] = useState("");
  const [selectedActionPath, setSelectedActionPath] = useState("");
  const [hoveredActionPath, setHoveredActionPath] = useState("");

  useEffect(() => {
    if (!branchMenuOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (!branchMenuRef.current?.contains(event.target as Node)) {
        setBranchMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [branchMenuOpen]);

  useEffect(() => {
    if (!branchMenuOpen || !rootId) {
      return;
    }
    let cancelled = false;
    setBranchesLoading(true);
    void fetchGitBranches(rootId)
      .then((payload) => {
        if (!cancelled) {
          setBranches(payload.branches || []);
        }
      })
      .catch((err) => {
        console.error("[git.branches] failed", { rootId, err });
        if (!cancelled) {
          setBranches([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setBranchesLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [branchMenuOpen, rootId]);

  const runPanelAction = async (key: string, action?: () => void | Promise<void>) => {
    if (!action || actionBusy) {
      return;
    }
    setActionBusy(key);
    try {
      await action();
    } catch {
      // The caller owns user-facing error reporting; keep local button state consistent.
    } finally {
      setActionBusy("");
    }
  };

  const submitCommit = async () => {
    const message = commitMessage.trim();
    if (!message || !onCommit || actionBusy) {
      return;
    }
    await runPanelAction("commit", async () => {
      await onCommit(message);
      setCommitMessage("");
      setCommitOpen(false);
    });
  };

  const discardItem = async (item: GitStatusItem, onDiscard?: (item: GitStatusItem) => void | Promise<void>) => {
    const runDiscard = () => runPanelAction(`discard:${item.path}`, () => onDiscard?.(item));
    if (item.status !== "??") {
      await runDiscard();
      return;
    }
    const ok = await confirmDialog({
      message: t("git.confirmDeleteUntracked", { path: item.path }),
      danger: true,
    });
    if (ok) await runDiscard();
  };

  return {
    branchMenuRef,
    branchMenuOpen,
    branches,
    branchesLoading,
    switchingBranch,
    commitOpen,
    commitMessage,
    actionBusy,
    selectedActionPath,
    hoveredActionPath,
    setBranchMenuOpen,
    setSwitchingBranch,
    setCommitOpen,
    setCommitMessage,
    setActionBusy,
    setSelectedActionPath,
    setHoveredActionPath,
    runPanelAction,
    discardItem,
    submitCommit,
  };
}
