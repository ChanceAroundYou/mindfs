import { useEffect, useState } from "react";
import { fetchProofProtectedBlob } from "../services/file";

export function useProtectedBlobUrl(request?: { rootId: string; path: string; nodeId?: string }): string {
  const [url, setURL] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";
    if (!request) return undefined;
    const currentRequest = request;

    async function load() {
      try {
        const blob = await fetchProofProtectedBlob(currentRequest);
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setURL(objectURL);
      } catch {
        if (!cancelled) setURL("");
      }
    }

    void load();
    return () => {
      cancelled = true;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [request?.path, request?.rootId, request?.nodeId]);

  return url;
}
