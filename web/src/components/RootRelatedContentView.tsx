import React from "react";
import { useI18n } from "../i18n";
import { relatedFileStatKey } from "../hooks/useRelatedFileStats";
import type { RelatedFileStat, RelatedFileStatTarget } from "../hooks/useRelatedFileStats";
import { ManagedRootPayload } from "../app/appMisc";
import { GitFileStat, SessionItem } from "../app/appSession";
import type { KanbanTask } from "../services/tasks";

/** 与 App 里 selectedSessionRelatedFileGroups 的 flatMap 结果同构。 */
type RelatedFileEntry = RelatedFileStatTarget & {
  path: string;
  name: string;
  repo_name: string;
  root_id: string;
};

type RelatedFileGroup = {
  key: string;
  head: string;
  repoPath: string;
  repoName: string;
  repoKind: string;
  files: RelatedFileEntry[];
};

/**
 * 项目树「相关文件」页签的内容：当前会话/任务关联到的文件，按仓库与提交分组。
 *
 * 纯视图——数据与回调全部由 App 传入。分组与统计在上游 useMemo 里算好，
 * 这里只负责渲染与交互回调。
 */
export function RootRelatedContentView({
  root,
  currentRootId,
  relatedSessionRootId,
  relatedSessionSnapshot,
  relatedSessionKey,
  relatedSelectedPath,
  relatedSelectedFileKey,
  relatedFileSelectionKey,
  managedRootByIdRef,
  selectedKanbanTask,
  selectedSessionRelatedFiles,
  selectedSessionRelatedFileGroups,
  selectedRelatedFileStatsByKey,
  gitFileStatsByPath,
  normalizePath,
  handleSelectedSessionFileClick,
  handleRemoveSessionRelatedFile,
}: {
  root: string;
  currentRootId: string | null;
  relatedSessionRootId: string | null;
  relatedSessionSnapshot: SessionItem | null;
  relatedSessionKey: string;
  relatedSelectedPath: string;
  relatedSelectedFileKey: string;
  relatedFileSelectionKey: (file: RelatedFileEntry) => string;
  managedRootByIdRef: React.MutableRefObject<Record<string, ManagedRootPayload>>;
  selectedKanbanTask: KanbanTask | null;
  selectedSessionRelatedFiles: RelatedFileEntry[];
  selectedSessionRelatedFileGroups: RelatedFileGroup[];
  selectedRelatedFileStatsByKey: Record<string, RelatedFileStat>;
  gitFileStatsByPath: Record<string, GitFileStat>;
  normalizePath: (value: string) => string;
  handleSelectedSessionFileClick: (target: string | RelatedFileEntry) => void;
  handleRemoveSessionRelatedFile: (
    rootId: string | null | undefined,
    sessionKey: string | undefined,
    path: string,
    head?: string,
    repoPath?: string,
    repoKind?: string,
  ) => Promise<void>;
}) {
  const { t } = useI18n();

    if (!root || root !== currentRootId || root !== relatedSessionRootId) {
      return null;
    }
    if (!relatedSessionSnapshot && !selectedKanbanTask) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("session.empty")}
        </div>
      );
    }
    if (selectedSessionRelatedFiles.length === 0) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("session.relatedFiles", { count: 0 })}
        </div>
      );
    }
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "4px", minWidth: 0 }}>
        {selectedSessionRelatedFileGroups.map((group) => {
          const isCurrentRepo =
            !group.repoPath ||
            normalizePath(group.repoPath) ===
              normalizePath(managedRootByIdRef.current[root]?.root_path || "");
          const showGroupHeader = group.repoKind === "plain" || group.head || !isCurrentRepo;
          return (
            <div key={group.key} style={{ display: "flex", flexDirection: "column", gap: "4px", minWidth: 0 }}>
              {showGroupHeader ? (
                <div
                  title={[group.repoPath, group.head].filter(Boolean).join(" · ") || group.repoName || t("session.currentProject")}
                  style={{
                    padding: "3px 6px 0",
                    fontSize: "11px",
                    color: "var(--text-secondary)",
                    fontFamily: group.head ? "var(--mono-font, monospace)" : undefined,
                  }}
                >
                  {group.repoKind === "plain"
                    ? `${group.repoName || t("session.currentProject")} · ${t("session.nonGit")}`
                    : group.head
                      ? isCurrentRepo
                        ? `HEAD ${group.head.slice(0, 8)}`
                        : `${group.repoName || t("session.currentProject")} · HEAD ${group.head.slice(0, 8)}`
                      : group.repoName || t("session.currentProject")}
                </div>
              ) : null}
              {group.files.map((file) => {
          const stats = selectedRelatedFileStatsByKey[relatedFileStatKey(file)] || gitFileStatsByPath[file.path];
          const fileSelectionKey = relatedFileSelectionKey(file);
          const isSelected = relatedSelectedFileKey
            ? fileSelectionKey === relatedSelectedFileKey
            : file.path === relatedSelectedPath;

  return (
            <div key={`${file.head || "legacy"}:${file.path}`} style={{ display: "flex", alignItems: "center", gap: "4px", minWidth: 0 }}>
              <button
                type="button"
                onClick={() => handleSelectedSessionFileClick(file)}
                title={file.path}
                style={{
                  flex: 1,
                  minWidth: 0,
                  border: "none",
                  background: isSelected ? "var(--selection-bg)" : "transparent",
                  color: isSelected ? "var(--accent-color)" : "var(--text-primary)",
                  borderRadius: "6px",
                  padding: "5px 6px",
                  display: "flex",
                  alignItems: "center",
                  gap: "7px",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                  fontWeight: isSelected ? 700 : 400,
                }}
              >
                <span style={{ width: "16px", display: "inline-flex", justifyContent: "center", color: "#94a3b8", flexShrink: 0 }}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                    <polyline points="14 2 14 8 20 8" />
                    <line x1="16" x2="8" y1="13" y2="13" />
                    <line x1="16" x2="8" y1="17" y2="17" />
                  </svg>
                </span>
                <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {file.name}
                </span>
                {stats ? (
                  <span style={{ display: "inline-flex", alignItems: "center", gap: "5px", fontSize: "11px", color: "var(--text-secondary)", flexShrink: 0 }}>
                    <span style={{ color: "#15803d", fontVariantNumeric: "tabular-nums" }}>+{stats.additions}</span>
                    <span style={{ color: "#b91c1c", fontVariantNumeric: "tabular-nums" }}>-{stats.deletions}</span>
                  </span>
                ) : null}
              </button>
              <button
                type="button"
                aria-label={t("session.removeRelatedFile", { name: file.name || file.path })}
                title={t("session.removeRelatedFile", { name: file.name || file.path })}
                onClick={(event) => {
                  event.stopPropagation();
                  void handleRemoveSessionRelatedFile(
                    relatedSessionRootId,
                    relatedSessionKey,
                    file.path,
                    file.head,
                    file.repo_path,
                    file.repo_kind,
                  );
                }}
                style={{
                  width: "18px",
                  height: "18px",
                  border: "none",
                  borderRadius: "5px",
                  background: "transparent",
                  color: "#dc2626",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  padding: 0,
                  cursor: "pointer",
                  flexShrink: 0,
                  fontSize: "13px",
                }}
              >
                x
              </button>
            </div>
          );
              })}
            </div>
          );
        })}
      </div>
  );
}
