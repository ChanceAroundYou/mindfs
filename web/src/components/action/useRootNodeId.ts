import { useState } from "react";
import { getRootNodeId } from "../../services/net/rootNode";

export function useRootNodeId(rootId: string) {
  const [nodeId, setNodeId] = useState(() => getRootNodeId(rootId));
  const current = getRootNodeId(rootId);
  if (current !== nodeId) setNodeId(current);
  return nodeId;
}
