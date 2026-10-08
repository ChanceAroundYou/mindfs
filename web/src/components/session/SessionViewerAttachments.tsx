import React, { useEffect, useState } from "react";
import { fetchProofProtectedBlob } from "../../services/file";
import { getRootNodeId } from "../../services/net/rootNode";
import type { UploadAttachment } from "./sessionViewerTypes";

export function AttachmentImage({
  rootId,
  path,
  name,
}: {
  rootId: string;
  path: string;
  name: string;
}) {
  const [url, setURL] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";
    async function run() {
      try {
        const blob = await fetchProofProtectedBlob({ rootId, path, nodeId: String(getRootNodeId(rootId) || "").trim() || undefined });
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setURL(objectURL);
      } catch {
        if (!cancelled) {
          setURL("");
        }
      }
    }
    if (rootId && path) {
      void run();
    }
    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [rootId, path]);

  return (
    <img
      src={url}
      alt={name}
      style={{
        display: "block",
        width: "100%",
        maxHeight: "220px",
        objectFit: "cover",
        background: "rgba(15,23,42,0.06)",
      }}
    />
  );
}
