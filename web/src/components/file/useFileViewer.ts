import React, { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { useI18n } from "../../i18n/index";
import { getDocumentPreviewKind } from "../../services/documentPreview";
import { supportsLineSelection } from "./CodeViewer";
import { downloadFile } from "../../services/platform/download";
import { isNativeShellRuntime } from "../../services/platform/runtime";
import type { FilePayload } from "../../services/file";
import { FileEditStore, fileEditKey, MAX_EDITABLE_FILE_BYTES } from "../../services/fileEditing";

type RelatedSession = {
  source_session: string;
  session_name?: string;
  agent?: string;
  created_at?: string;
  updated_at?: string;
};

export type FileViewerProps = {
  rootPath?: string;
  onFileOperationComplete?: () => void | Promise<void>;
  editStore: FileEditStore;
  onFileUpdated: (file: FilePayload) => void;
  onFileSaved: (file: FilePayload) => void;
  file?: FilePayload | null;
  rootDisplayName?: string | null;
  rootColor?: string | null;
  onSessionClick?: (sessionKey: string) => void;
  onPathClick?: (path: string) => void;
  onFileClick?: (path: string) => void;
  onSelectionChange?: (selection: {
    filePath: string;
    text?: string;
    startLine?: number;
    endLine?: number;
  } | null) => void;
  initialScrollTop?: number;
  onScrollTopChange?: (scrollTop: number) => void;
  isVisible?: boolean;
};

function isSelectionInside(root: Node, range: Range): boolean {
  return root.contains(range.commonAncestorContainer)
    || root.contains(range.startContainer)
    || root.contains(range.endContainer);
}

function countLineAtOffset(content: string, offset: number): number {
  let line = 1;
  const limit = Math.max(0, Math.min(offset, content.length));
  for (let i = 0; i < limit; i += 1) {
    if (content.charCodeAt(i) === 10) {
      line += 1;
    }
  }
  return line;
}

function getSelectionOffsets(root: Node, range: Range): { start: number; end: number } | null {
  try {
    const startRange = document.createRange();
    startRange.selectNodeContents(root);
    startRange.setEnd(range.startContainer, range.startOffset);
    const endRange = document.createRange();
    endRange.selectNodeContents(root);
    endRange.setEnd(range.endContainer, range.endOffset);
    return {
      start: startRange.toString().length,
      end: endRange.toString().length,
    };
  } catch {
    return null;
  }
}

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

export function useFileViewer({
  rootPath,
  onFileOperationComplete,
  editStore,
  onFileUpdated,
  onFileSaved,
  file,
  rootDisplayName = null,
  rootColor = null,
  onSessionClick,
  onPathClick,
  onFileClick,
  onSelectionChange,
  initialScrollTop = 0,
  onScrollTopChange,
  isVisible = true,
}: FileViewerProps) {
  const { t } = useI18n();
  const editKey = fileEditKey(file?.root || "", file?.path || "");
  const editSession = useSyncExternalStore(editStore.subscribe, () => editStore.get(editKey));
  const canEdit = !!file?.root && file.encoding === "utf-8" && file.size <= MAX_EDITABLE_FILE_BYTES
    && !/^(?:\/|[A-Za-z]:)/.test(file.path);
  const [isDownloading, setIsDownloading] = useState(false);
  const [editorActions, setEditorActions] = useState<HTMLSpanElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const restoredScrollKeyRef = useRef("");
  const contentRootRef = useRef<HTMLDivElement | null>(null);
  const codeContentRef = useRef<HTMLElement | null>(null);
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.innerWidth <= 768;
  });

  const fileScrollKey = file ? `${file.root || ""}::${file.path}` : "";

  useEffect(() => {
    if (typeof window === "undefined") return undefined;
    const media = window.matchMedia("(max-width: 768px)");
    const update = () => setIsMobile(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  useLayoutEffect(() => {
    if (editSession) { restoredScrollKeyRef.current = ""; return; }
    if (!isVisible) return;
    if (!fileScrollKey || !scrollRef.current) return;
    if (file?.targetLine && file.targetLine > 0) return;
    const savedTop = typeof initialScrollTop === "number" ? initialScrollTop : 0;
    if (savedTop <= 0) return;
    if (restoredScrollKeyRef.current === `${fileScrollKey}:${savedTop}`) return;
    let cancelled = false;
    let frame1 = 0;
    let frame2 = 0;
    const applyScrollTop = () => {
      if (cancelled || !scrollRef.current) return;
      scrollRef.current.scrollTop = savedTop;
    };
    frame1 = window.requestAnimationFrame(() => {
      applyScrollTop();
      frame2 = window.requestAnimationFrame(() => {
        applyScrollTop();
        restoredScrollKeyRef.current = `${fileScrollKey}:${savedTop}`;
      });
    });
    return () => {
      cancelled = true;
      window.cancelAnimationFrame(frame1);
      window.cancelAnimationFrame(frame2);
    };
  }, [fileScrollKey, file?.targetLine, file?.content, initialScrollTop, isVisible, !!editSession]);

  useEffect(() => {
    const node = scrollRef.current;
    if (!node || !fileScrollKey) return;
    const handleScroll = () => {
      onScrollTopChange?.(node.scrollTop);
    };
    node.addEventListener("scroll", handleScroll, { passive: true });
    return () => node.removeEventListener("scroll", handleScroll);
  }, [fileScrollKey, onScrollTopChange, !!editSession]);

  const ext = file?.ext || (file?.path.includes(".") ? `.${file.path.split(".").pop()}` : "");
  const documentPreviewKind = getDocumentPreviewKind(ext, file?.mime);
  const usesMarkdownViewer = ext === ".md" || ext === ".markdown";
  const lineSelectionEnabled = !editSession && !!file
    && !usesMarkdownViewer
    && file.encoding !== "binary"
    && supportsLineSelection(ext);

  const updateSelection = useCallback(() => {
    if (!file || !onSelectionChange || editSession) {
      return;
    }
    if (file.encoding === "binary" || file.mime?.startsWith("image/")) {
      onSelectionChange(null);
      return;
    }
    const root = (usesMarkdownViewer ? contentRootRef.current : codeContentRef.current) as Node | null;
    const selection = window.getSelection();
    if (!root || !selection || selection.rangeCount === 0 || selection.isCollapsed) {
      onSelectionChange(null);
      return;
    }
    const range = selection.getRangeAt(0);
    if (!isSelectionInside(root, range)) {
      onSelectionChange(null);
      return;
    }
    const text = selection.toString();
    if (!text.trim()) {
      onSelectionChange(null);
      return;
    }
    if (lineSelectionEnabled) {
      const offsets = getSelectionOffsets(root, range);
      if (!offsets) {
        onSelectionChange(null);
        return;
      }
      const startLine = countLineAtOffset(file.content, offsets.start);
      const endLine = countLineAtOffset(file.content, Math.max(offsets.start, offsets.end - 1));
      onSelectionChange({
        filePath: file.path,
        text,
        startLine,
        endLine,
      });
      return;
    }
    onSelectionChange({
      filePath: file.path,
      text,
    });
  }, [file, onSelectionChange, lineSelectionEnabled, usesMarkdownViewer, editSession]);

  useEffect(() => {
    if (!onSelectionChange) {
      return;
    }
    if (editSession || !file || file.encoding === "binary" || file.mime?.startsWith("image/")) {
      onSelectionChange(null);
      return;
    }
    const handleSelectionChange = () => {
      updateSelection();
    };
    document.addEventListener("selectionchange", handleSelectionChange);
    return () => {
      document.removeEventListener("selectionchange", handleSelectionChange);
      onSelectionChange(null);
    };
  }, [file, onSelectionChange, updateSelection, editSession]);

  const [downloadToast, setDownloadToast] = useState<{ msg: string; ok: boolean } | null>(null);

  const showToast = useCallback((msg: string, ok: boolean) => {
    setDownloadToast({ msg, ok });
    window.setTimeout(() => setDownloadToast(null), 3000);
  }, []);

  const handleDownload = useCallback(async () => {
    if (!file?.root || !file.path || isDownloading) {
      return;
    }
    try {
      setIsDownloading(true);
      await downloadFile({
        rootId: file.root,
        path: file.path,
        name: file.name,
      });
      showToast(isNativeShellRuntime() ? t("fileViewer.downloadSavedNative") : t("fileViewer.downloadStarted"), true);
    } catch (error) {
      const message = error instanceof Error ? error.message : t("fileViewer.downloadFailed");
      showToast(message, false);
    } finally {
      setIsDownloading(false);
    }
  }, [file, isDownloading, showToast, t]);

  const relatedSessions = normalizeRelatedSessions((file as any)?.file_meta);
  const visibleRelatedSessions = relatedSessions.slice(0, isMobile ? 2 : 3);

  return {
    editKey,
    editSession,
    canEdit,
    isDownloading,
    setIsDownloading,
    editorActions,
    setEditorActions,
    scrollRef,
    contentRootRef,
    codeContentRef,
    isMobile,
    fileScrollKey,
    ext,
    documentPreviewKind,
    usesMarkdownViewer,
    lineSelectionEnabled,
    downloadToast,
    showToast,
    handleDownload,
    relatedSessions,
    visibleRelatedSessions,
  };
}
