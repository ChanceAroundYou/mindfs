import { useEffect, useMemo, useRef, useState } from "react";
import type { SessionItem } from "./sessionViewerTypes";
import { shouldDefaultCollapseRelatedFiles } from "./sessionViewerFormat";
import { groupRelatedFiles } from "./sessionViewerGrouping";
import { findActiveAskUserCallId } from "./sessionViewerAskUser";
import { scrollToRelatedDivider } from "./sessionViewerScroll";
import { relatedFileStatKey, useRelatedFileStats } from "../../hooks/useRelatedFileStats";
import type { TimelineItem } from "../../hooks/useSessionStream";

export function useRelatedFiles({
  session,
  timeline,
  isAwaiting,
  rootId,
  rootPath,
  gitFileStatsByPath,
  sessionNodeId,
  isMobile,
  t,
  interactionMode,
  activeScrollRef,
}: {
  session: SessionItem | null;
  timeline: TimelineItem[];
  isAwaiting: boolean;
  rootId: string | null | undefined;
  rootPath: string | null | undefined;
  gitFileStatsByPath: Record<string, { status: string; additions: number; deletions: number }>;
  sessionNodeId: string | undefined;
  isMobile: boolean;
  t: (key: any, fallback?: any) => string;
  interactionMode: string;
  activeScrollRef: React.RefObject<HTMLDivElement | null>;
}) {
  const [relatedFilesCollapsed, setRelatedFilesCollapsed] = useState(true);
  const relatedFilesDividerRef = useRef<HTMLDivElement | null>(null);
  const relatedFilesDefaultStateRef = useRef<string>("");

  const rawRelated = session?.related_files || (session as any)?.outputs || [];
  const relatedFiles = (Array.isArray(rawRelated) ? rawRelated : [])
    .map((f: any) => {
      const path =
        typeof f === "string" ? f : typeof f?.path === "string" ? f.path : "";
      const name =
        typeof f?.name === "string" ? f.name : path.split("/").pop() || path;
      const head =
        typeof f !== "string" && typeof f?.head === "string" ? f.head : "";
      const repoPath =
        typeof f !== "string" && typeof f?.repo_path === "string"
          ? f.repo_path
          : "";
      const repoName =
        typeof f !== "string" && typeof f?.repo_name === "string"
          ? f.repo_name
          : repoPath.split(/[\\/]/).filter(Boolean).pop() || "";
      const repoKind =
        typeof f !== "string" && typeof f?.repo_kind === "string"
          ? f.repo_kind
          : "";
      const rootID =
        typeof f !== "string" && typeof f?.root_id === "string"
          ? f.root_id
          : "";
      return { path, name, head, repo_path: repoPath, repo_name: repoName, repo_kind: repoKind, root_id: rootID };
    })
    .filter((f) => f.path);

  const gitStatsRefreshKey = Object.entries(gitFileStatsByPath)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([path, stats]) => `${path}:${stats.status}:${stats.additions}:${stats.deletions}`)
    .join("|");

  const relatedFileStatsByKey = useRelatedFileStats(
    rootId,
    relatedFiles,
    gitStatsRefreshKey,
    sessionNodeId,
  );

  const scrollToRelatedFilesDividerFn = (behavior: ScrollBehavior = "auto") =>
    scrollToRelatedDivider({
      behavior,
      activeScrollRef,
      interactionMode,
      relatedFilesDividerRef,
      relatedFiles,
      relatedFilesCollapsed,
    });

  const activeAskUserCallId = findActiveAskUserCallId(timeline, isAwaiting);
  const defaultRelatedFilesCollapsed = shouldDefaultCollapseRelatedFiles(
    isMobile,
    relatedFiles.length,
  );
  const sessionKey = session?.key || session?.session_key || null;
  const relatedFilesDefaultStateKey = `${sessionKey || ""}:${isMobile ? "mobile" : "desktop"}:${defaultRelatedFilesCollapsed ? "collapsed" : "expanded"}`;

  useEffect(() => {
    if (relatedFilesDefaultStateRef.current === relatedFilesDefaultStateKey) {
      return;
    }
    relatedFilesDefaultStateRef.current = relatedFilesDefaultStateKey;
    setRelatedFilesCollapsed(defaultRelatedFilesCollapsed);
  }, [defaultRelatedFilesCollapsed, relatedFilesDefaultStateKey]);

  const displayFileGroups = groupRelatedFiles(relatedFiles, rootPath, t);

  return {
    relatedFiles,
    relatedFileStatsByKey,
    gitStatsRefreshKey,
    scrollToRelatedFilesDivider: scrollToRelatedFilesDividerFn,
    activeAskUserCallId,
    relatedFilesCollapsed,
    setRelatedFilesCollapsed,
    relatedFilesDividerRef,
    displayFileGroups,
    relatedFileStatKey,
  };
}
