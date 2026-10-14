import { useCallback } from "react";
import { fetchFile } from "../../services/file";
import type { FilePayload } from "../../shared/fileTypes";

export function useFileEditorReload() {
  return useCallback((rootId: string, path: string): Promise<FilePayload | null> => (
    fetchFile({ rootId, path, readMode: "full", fresh: true, timeoutMs: 15000 })
  ), []);
}
