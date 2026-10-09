import React, { useEffect, useState } from "react";
import {
  fetchGitCommitFiles,
  type GitHistoryItem,
  type GitStatusItem,
} from "../../services/git";

export type GitHistoryPanelProps = {
  rootId: string;
  items: GitHistoryItem[];
  loading?: boolean;
  loadingMore?: boolean;
  hasMore?: boolean;
  compact?: boolean;
  expandedCommits?: Record<string, boolean>;
  onToggleCommit?: (hash: string) => void;
  onLoadMore?: () => void;
  onSelectFile?: (commit: GitHistoryItem, item: GitStatusItem) => void;
};

export function useGitHistoryPanel({
  rootId,
  items,
  expandedCommits = {},
  onToggleCommit,
}: GitHistoryPanelProps) {
  const [filesByCommit, setFilesByCommit] = useState<Record<string, GitStatusItem[]>>({});
  const [loadingFiles, setLoadingFiles] = useState<Record<string, boolean>>({});

  const loadCommitFiles = (commit: GitHistoryItem) => {
    if (filesByCommit[commit.hash] || loadingFiles[commit.hash]) {
      return;
    }
    setLoadingFiles((prev) => ({ ...prev, [commit.hash]: true }));
    void fetchGitCommitFiles(rootId, commit.hash)
      .then((payload) => {
        setFilesByCommit((prev) => ({ ...prev, [commit.hash]: payload.items || [] }));
      })
      .catch((err) => {
        console.error("[git.commit.files] failed", { rootId, commit: commit.hash, err });
        setFilesByCommit((prev) => ({ ...prev, [commit.hash]: [] }));
      })
      .finally(() => {
        setLoadingFiles((prev) => ({ ...prev, [commit.hash]: false }));
      });
  };

  useEffect(() => {
    items.forEach((commit) => {
      if (expandedCommits[commit.hash]) {
        loadCommitFiles(commit);
      }
    });
  }, [expandedCommits, items]);

  const toggleCommit = (commit: GitHistoryItem) => {
    onToggleCommit?.(commit.hash);
    if (!expandedCommits[commit.hash]) {
      loadCommitFiles(commit);
    }
  };

  return {
    filesByCommit,
    loadingFiles,
    toggleCommit,
  };
}
