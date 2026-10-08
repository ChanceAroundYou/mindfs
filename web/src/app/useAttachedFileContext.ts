/**
 * 文件上下文选择 —— F 簇（~101 行）。
 *
 * 覆盖：buildAttachedFileContext、handleRequestFileContext、
 * handleClearFileContext、handleViewerSelectionChange + 2 个 useEffect。
 *
 * 做法：hook，参数面 UseAttachedFileContextParams 即完整契约；函数体逐字搬入。
 */
import { useCallback, useEffect, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { AttachedFileContext, ViewerSelection } from "./appSession";

export interface UseAttachedFileContextParams {
  currentSelectionSource: { path: string; name: string } | null;
  viewerSelection: ViewerSelection | null;
  dismissedSelectionFileRef: MutableRefObject<string | null>;
  viewerSelectionRef: MutableRefObject<ViewerSelection | null>;
  lastViewerSelectionRef: MutableRefObject<ViewerSelection | null>;
  setAttachedFileContext: Dispatch<SetStateAction<AttachedFileContext | null>>;
  setViewerSelection: Dispatch<SetStateAction<ViewerSelection | null>>;
}

export function useAttachedFileContext(params: UseAttachedFileContextParams) {
  const {
    currentSelectionSource,
    viewerSelection,
    dismissedSelectionFileRef,
    viewerSelectionRef,
    lastViewerSelectionRef,
    setAttachedFileContext,
    setViewerSelection,
  } = params;

  const buildAttachedFileContext = useCallback(
    (
      currentSource: { path: string; name: string } | null,
      selection: ViewerSelection | null,
    ): AttachedFileContext | null => {
      if (!currentSource?.path) {
        return null;
      }
      const matchesCurrentFile = selection?.filePath === currentSource.path;
      return {
        filePath: currentSource.path,
        fileName: currentSource.name,
        startLine: matchesCurrentFile ? selection?.startLine : undefined,
        endLine: matchesCurrentFile ? selection?.endLine : undefined,
        text: matchesCurrentFile ? selection?.text : undefined,
      };
    },
    [],
  );

  const handleRequestFileContext = useCallback(() => {
    const currentSource = currentSelectionSource;
    if (
      dismissedSelectionFileRef.current &&
      dismissedSelectionFileRef.current === currentSource?.path
    ) {
      return;
    }
    const liveSelection = viewerSelectionRef.current;
    const fallbackSelection = lastViewerSelectionRef.current;
    const nextSelection =
      liveSelection?.filePath === currentSource?.path
        ? liveSelection
        : fallbackSelection?.filePath === currentSource?.path
          ? fallbackSelection
          : null;
    const next = buildAttachedFileContext(currentSource, nextSelection);
    setAttachedFileContext(next);
  }, [buildAttachedFileContext, currentSelectionSource]);

  const handleClearFileContext = useCallback(() => {
    dismissedSelectionFileRef.current = currentSelectionSource?.path || null;
    lastViewerSelectionRef.current = null;
    setAttachedFileContext(null);
  }, [currentSelectionSource]);

  const handleViewerSelectionChange = useCallback(
    (next: ViewerSelection | null) => {
      setViewerSelection(next);
      if (next?.filePath) {
        dismissedSelectionFileRef.current = null;
        lastViewerSelectionRef.current = next;
      }
    },
    [],
  );

  useEffect(() => {
    const currentPath = currentSelectionSource?.path;
    if (!currentPath) {
      dismissedSelectionFileRef.current = null;
      lastViewerSelectionRef.current = null;
      setViewerSelection(null);
      setAttachedFileContext(null);
      return;
    }
    if (
      dismissedSelectionFileRef.current &&
      dismissedSelectionFileRef.current !== currentPath
    ) {
      dismissedSelectionFileRef.current = null;
    }
    if (lastViewerSelectionRef.current?.filePath !== currentPath) {
      lastViewerSelectionRef.current = null;
    }
    setViewerSelection((prev) =>
      prev?.filePath === currentPath ? prev : null,
    );
    setAttachedFileContext((prev) => {
      if (!prev) {
        return prev;
      }
      if (prev.filePath !== currentPath) {
        return null;
      }
      return prev;
    });
  }, [currentSelectionSource?.path]);

  useEffect(() => {
    const currentPath = currentSelectionSource?.path;
    setAttachedFileContext((prev) => {
      if (!prev || prev.filePath !== currentPath) {
        return prev;
      }
      if (!viewerSelection || viewerSelection.filePath !== currentPath) {
        return prev;
      }
      return buildAttachedFileContext(currentSelectionSource, viewerSelection);
    });
  }, [buildAttachedFileContext, currentSelectionSource, viewerSelection]);

  return {
    buildAttachedFileContext,
    handleRequestFileContext,
    handleClearFileContext,
    handleViewerSelectionChange,
  };
}
