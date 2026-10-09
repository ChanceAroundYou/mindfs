import React, { useEffect, useMemo, useRef, useState } from "react";
import type { GitDiffPayload } from "../../services/git";
import {
  buildDiffLines,
  buildSideBySideRows,
  buildUnifiedRows,
} from "../../shared/gitDiffModel";

type RelatedSession = {
  source_session: string;
  session_name?: string;
  agent?: string;
  created_at?: string;
  updated_at?: string;
};

export type GitDiffViewerProps = {
  diff: GitDiffPayload;
  root?: string | null;
  rootDisplayName?: string | null;
  rootColor?: string | null;
  sideBySide?: boolean;
  onPathClick?: (path: string) => void;
  onSessionClick?: (sessionKey: string) => void;
  onSelectionChange?: (selection: {
    filePath: string;
    text?: string;
    startLine?: number;
    endLine?: number;
  } | null) => void;
};

function normalizeRelatedSessions(raw: unknown): RelatedSession[] {
  if (!raw) return [];
  const list = Array.isArray(raw) ? raw : [raw];
  const normalized = list.map((item) => {
    if (!item || typeof item !== "object") return null;
    const value = item as Record<string, unknown>;
    const source = (typeof value.source_session === "string" && value.source_session) || (typeof value.sourceSession === "string" && value.sourceSession) || (typeof value.session_key === "string" && value.session_key) || "";
    if (!source) return null;
    return {
      source_session: source,
      session_name: (typeof value.session_name === "string" && value.session_name) || undefined,
      agent: typeof value.agent === "string" ? value.agent : undefined,
      created_at: typeof value.created_at === "string" ? value.created_at : undefined,
      updated_at: typeof value.updated_at === "string" ? value.updated_at : undefined,
    };
  }).filter((v): v is NonNullable<typeof v> => v !== null) as RelatedSession[];
  const dedup = new Map<string, RelatedSession>();
  normalized.forEach((item) => {
    const existing = dedup.get(item.source_session);
    if (!existing) {
      dedup.set(item.source_session, item);
      return;
    }
    const existingTime = Date.parse(existing.updated_at || existing.created_at || "") || 0;
    const itemTime = Date.parse(item.updated_at || item.created_at || "") || 0;
    if (itemTime >= existingTime) {
      dedup.set(item.source_session, item);
    }
  });
  return Array.from(dedup.values()).sort((left, right) => {
    const leftTime = Date.parse(left.updated_at || left.created_at || "") || 0;
    const rightTime = Date.parse(right.updated_at || right.created_at || "") || 0;
    return rightTime - leftTime;
  });
}

export function useGitDiffViewer({
  diff,
  onSelectionChange,
}: GitDiffViewerProps) {
  const lines = useMemo(() => buildDiffLines(diff.content), [diff.content]);
  const sideBySideRows = useMemo(() => buildSideBySideRows(lines), [lines]);
  const unifiedRows = useMemo(() => buildUnifiedRows(lines), [lines]);
  const relatedSessions = useMemo(() => normalizeRelatedSessions(diff.file_meta), [diff.file_meta]);
  const displayPath = diff.display_path || diff.path;
  const contentRootRef = useRef<HTMLDivElement | null>(null);
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.innerWidth <= 768;
  });

  const visibleRelatedSessions = relatedSessions.slice(0, isMobile ? 2 : 3);

  useEffect(() => {
    if (typeof window === "undefined") return undefined;
    const media = window.matchMedia("(max-width: 768px)");
    const update = () => setIsMobile(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  useEffect(() => {
    const updateSelection = () => {
      const rootNode = contentRootRef.current;
      if (!onSelectionChange || !rootNode) {
        return;
      }
      const selection = window.getSelection();
      if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
        onSelectionChange(null);
        return;
      }
      const range = selection.getRangeAt(0);
      if (!rootNode.contains(range.commonAncestorContainer)
        && !rootNode.contains(range.startContainer)
        && !rootNode.contains(range.endContainer)) {
        onSelectionChange(null);
        return;
      }
      const text = selection.toString();
      if (!text.trim()) {
        onSelectionChange(null);
        return;
      }
      const rows = Array.from(rootNode.querySelectorAll<HTMLElement>("[data-line-number]"));
      const lineNumbers = rows
        .filter((row) => {
          try {
            return range.intersectsNode(row);
          } catch {
            return false;
          }
        })
        .map((row) => Number.parseInt(row.dataset.lineNumber || "", 10))
        .filter((value) => Number.isFinite(value));
      const startLine = lineNumbers.length > 0 ? Math.min(...lineNumbers) : undefined;
      const endLine = lineNumbers.length > 0 ? Math.max(...lineNumbers) : undefined;
      onSelectionChange({
        filePath: diff.path,
        text,
        startLine,
        endLine,
      });
    };

    if (!onSelectionChange) {
      return;
    }
    const handleSelectionChange = () => updateSelection();
    document.addEventListener("selectionchange", handleSelectionChange);
    return () => {
      document.removeEventListener("selectionchange", handleSelectionChange);
      onSelectionChange(null);
    };
  }, [diff.path, onSelectionChange]);

  return {
    lines,
    sideBySideRows,
    unifiedRows,
    relatedSessions,
    displayPath,
    contentRootRef,
    isMobile,
    visibleRelatedSessions,
  };
}
