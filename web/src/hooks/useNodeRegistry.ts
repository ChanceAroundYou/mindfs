import { useCallback, useEffect, useState } from "react";
import { getActiveNodeId, getNodes, setActiveNodeId, type NodeConnection } from "../services/nodeRegistry";

export function useNodeRegistry() {
  const [nodes, setNodes] = useState<NodeConnection[]>(() => getNodes());
  const [activeId, setActiveId] = useState<string | null>(() => getActiveNodeId());

  const refresh = useCallback(() => {
    setNodes(getNodes());
    setActiveId(getActiveNodeId());
  }, []);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (!e.key || e.key.startsWith("mindfs_nodes") || e.key.startsWith("mindfs_active")) refresh();
    };
    const onCustom = () => refresh();
    window.addEventListener("storage", onStorage);
    window.addEventListener("mindfs:nodes-changed", onCustom as EventListener);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener("mindfs:nodes-changed", onCustom as EventListener);
    };
  }, [refresh]);

  const setActive = useCallback((id: string | null) => {
    setActiveNodeId(id);
    window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
    refresh();
  }, [refresh]);

  // ponytail: 聚合视图已固定为 true，保留 setAggregated 仅兼容旧调用
  const setAggregated = useCallback((_v: boolean) => {}, []);

  return { nodes, activeId, aggregated: true as boolean, refresh, setActive, setAggregated };
}
