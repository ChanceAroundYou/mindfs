import { useEffect, useState } from "react";
import { fetchProofProtectedBlob } from "../../services/file";
import { describeDocumentPreviewError } from "../../shared/documentPreview";

type PreviewBlobState =
  | { status: "loading" }
  | { status: "ready"; blob: Blob }
  | { status: "error"; detail?: string };

export function useDocumentPreviewBlob(root: string | undefined, path: string): PreviewBlobState {
  const [state, setState] = useState<PreviewBlobState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    if (!root) {
      setState({ status: "error", detail: "no project" });
      return undefined;
    }
    void fetchProofProtectedBlob({ rootId: root, path })
      .then((blob) => {
        if (!cancelled) setState({ status: "ready", blob });
      })
      .catch((err) => {
        if (!cancelled) setState({ status: "error", detail: describeDocumentPreviewError(err) });
      });
    return () => {
      cancelled = true;
    };
  }, [path, root]);

  return state;
}
