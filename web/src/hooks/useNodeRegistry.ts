import { useCallback, useEffect, useState } from "react";
import { getActiveNodeId, getNodes, getAggregated, setActiveNodeId, setAggregated as persistAggregated, type NodeConnection } from "../services/nodeRegistry";

export function useNodeRegistry() {
  const [nodes, setNodes] = useState<NodeConnection[]>(() => getNodes());
  const [activeId, setActiveId] = useState<string | null>(() => getActiveNodeId());
  const [aggregated, setAggregatedState] = useState<boolean>(() => getAggregated());

  const refresh = useCallback(() => {
    setNodes(getNodes());
    setActiveId(getActiveNodeId());
    setAggregatedState(getAggregated());
  }, []);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (!e.key || e.key.startsWith("mindfs_nodes") || e.key.startsWith("mindfs_active") || e.key.startsWith("mindfs_aggregated")) refresh();
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

  const setAgg = useCallback((v: boolean) => {
    persistAggregated(v);
    window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
    refresh();
  }, [refresh]);

  return { nodes, activeId, aggregated, refresh, setActive, setAggregated: setAgg };
}
