import { useMemo } from "react";
import { useRelatedFileStats } from "../hooks/useRelatedFileStats";
import type { RelatedFile, RelatedWorktree } from "../services/session";
import type { KanbanTask } from "../services/tasks";
import type { MessageKey } from "../i18n";
import { normalizePath } from "./appPath";
import type { GitFileStat } from "./appSession";

/**
 * 会话/任务快照的宽松视图。四个来源（看板任务详情、主区会话、抽屉会话、上次主区快照）
 * 各自有细微差异，这里只声明本模块真正读取的字段。
 */
type RelatedSessionSnapshot =
  | {
      key?: string;
      session_key?: string;
      root_id?: string;
      related_files?: RelatedFile[];
      related_worktree?: RelatedWorktree | null;
      outputs?: unknown;
      _nodeId?: string;
    }
  | null
  | undefined;

type RootContentViewProps = {
  selectedKanbanTask: KanbanTask | null | undefined;
  taskRelatedFilesById: Record<string, RelatedFile[]>;
  relatedSessionSnapshot: RelatedSessionSnapshot;
  relatedSessionRootId: string | null | undefined;
  relatedSessionNodeId: string | undefined;
  currentRootId: string | null;
  managedRootByIdRef: { current: Record<string, any> };
  gitFileStatsByPath: Record<string, GitFileStat>;
  t: (key: MessageKey) => string;
};

/**
 * 「根内容」视图模型：工作树归属、会话关联文件、按仓库/HEAD 分组、git 统计刷新键。
 *
 * 全是纯派生值，没有副作用，也不参与任何 effect 时序 —— 抽出来只为让 App.tsx
 * 的渲染前计算有个明确的家。刻意不碰 handleProjectTreeRefresh / refresh 系列
 * （那些有 abort、force 语义，留在 App 里）。
 */
export function useRootContentView({
  selectedKanbanTask,
  taskRelatedFilesById,
  relatedSessionSnapshot,
  relatedSessionRootId,
  relatedSessionNodeId,
  currentRootId,
  managedRootByIdRef,
  gitFileStatsByPath,
  t,
}: RootContentViewProps) {
  // 任务侧优先，但不是无条件优先：任务记录的 worktree 目录已经被删时（worktree_missing，
  // 服务端派生），拿它去展开 git 面板只会打开一个不存在的目录 —— 而会话侧可能已经
  // repoint 干净、指向一个还活着的树。这种情况下让会话侧说话。
  const taskWorktree: RelatedWorktree | null =
    selectedKanbanTask?.worktree_path && selectedKanbanTask.worktree_missing !== true
      ? {
          root_id: selectedKanbanTask.root_id,
          path: selectedKanbanTask.worktree_path,
        }
      : null;
  const relatedWorktree: RelatedWorktree | null =
    taskWorktree || relatedSessionSnapshot?.related_worktree || null;

  const selectedSessionRelatedFiles = useMemo(() => {
    const rawRelated = selectedKanbanTask
      ? taskRelatedFilesById[selectedKanbanTask.id] || []
      : relatedSessionSnapshot?.related_files || (relatedSessionSnapshot as any)?.outputs || [];
    return (Array.isArray(rawRelated) ? rawRelated : [])
      .map((file: RelatedFile | string | { path?: unknown; name?: unknown; head?: unknown; repo_path?: unknown; repo_name?: unknown; repo_kind?: unknown; root_id?: unknown }) => {
        const path = typeof file === "string"
          ? file
          : typeof file?.path === "string"
            ? file.path
            : "";
        const rawName =
          typeof file !== "string" ? (file as { name?: unknown }).name : "";
        const name = typeof rawName === "string"
          ? rawName
          : path.split("/").pop() || path;
        const head = typeof file !== "string" && typeof file?.head === "string"
          ? file.head
          : "";
        const repoPath = typeof file !== "string" && typeof file?.repo_path === "string"
          ? file.repo_path
          : "";
        const repoName = typeof file !== "string" && typeof file?.repo_name === "string"
          ? file.repo_name
          : repoPath.split(/[\\/]/).filter(Boolean).pop() || "";
        const repoKind = typeof file !== "string" && typeof file?.repo_kind === "string"
          ? file.repo_kind
          : "";
        const rootID = typeof file !== "string" && typeof file?.root_id === "string"
          ? file.root_id
          : "";
        return { path, name, head, repo_path: repoPath, repo_name: repoName, repo_kind: repoKind, root_id: rootID };
      })
      .filter((file) => file.path);
  }, [relatedSessionSnapshot, selectedKanbanTask, taskRelatedFilesById]);

  const selectedSessionRelatedFileGroups = useMemo(
    () => {
      const currentRootPath = normalizePath(
        managedRootByIdRef.current[relatedSessionRootId || currentRootId || ""]?.root_path || "",
      );
      const repoGroups = selectedSessionRelatedFiles.reduce<
        Array<{
          key: string;
          repoPath: string;
          repoName: string;
          repoKind: string;
          headGroups: Array<{ key: string; head: string; files: typeof selectedSessionRelatedFiles }>;
        }>
      >((groups, file) => {
        const head = file.head || "";
        const rawRepoPath = file.repo_path || "";
        const isCurrentRepoRecord =
          !rawRepoPath ||
          file.repo_name === t("session.currentProject") ||
          (currentRootPath && normalizePath(rawRepoPath) === currentRootPath);
        const repoPath = isCurrentRepoRecord ? "" : rawRepoPath;
        const rawRepoKind = file.repo_kind || "";
        const repoKind = isCurrentRepoRecord && rawRepoKind !== "plain" ? "" : rawRepoKind;
        const repoKey = `${repoKind}\0${repoPath}`;
        let repoGroup = groups.find((group) => group.key === repoKey);
        if (!repoGroup) {
          repoGroup = {
            key: repoKey,
            repoPath,
            repoName: isCurrentRepoRecord
              ? t("session.currentProject")
              : file.repo_name || repoPath.split(/[\\/]/).filter(Boolean).pop() || t("session.currentProject"),
            repoKind,
            headGroups: [],
          };
          groups.push(repoGroup);
        }
        const headKey = `${repoKey}\0${head}`;
        const existing = repoGroup.headGroups.find((group) => group.key === headKey);
        if (existing) {
          existing.files.push(file);
        } else {
          repoGroup.headGroups.push({
            key: headKey,
            head,
            files: [file],
          });
        }
        return groups;
      }, []);
      return repoGroups.flatMap((repoGroup) =>
        repoGroup.headGroups.map((headGroup) => ({
          key: headGroup.key,
          head: headGroup.head,
          repoPath: repoGroup.repoPath,
          repoName: repoGroup.repoName,
          repoKind: repoGroup.repoKind,
          files: headGroup.files,
        })),
      );
    },
    [currentRootId, relatedSessionRootId, selectedSessionRelatedFiles, managedRootByIdRef, t],
  );

  const gitStatsRefreshKey = useMemo(
    () =>
      Object.entries(gitFileStatsByPath)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([path, stats]) => `${path}:${stats.status}:${stats.additions}:${stats.deletions}`)
        .join("|"),
    [gitFileStatsByPath],
  );

  const selectedRelatedFileStatsByKey = useRelatedFileStats(
    relatedSessionRootId || currentRootId,
    selectedSessionRelatedFiles,
    gitStatsRefreshKey,
    relatedSessionNodeId,
  );

  return {
    relatedWorktree,
    selectedSessionRelatedFiles,
    selectedSessionRelatedFileGroups,
    gitStatsRefreshKey,
    selectedRelatedFileStatsByKey,
  };
}
