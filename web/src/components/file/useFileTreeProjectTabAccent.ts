import { DEFAULT_NODE_COLOR } from "../../shared/nodePalette";
import { getNodes } from "../../services/net/nodeRegistry";

type AccentEntry = { name: string; _nodeColor?: string };

export function useFileTreeProjectTabAccent(
  rootColor: string | null | undefined,
  entries: AccentEntry[],
  rootId: string | null | undefined,
): string {
  const explicitColor = String(rootColor || "").trim();
  if (explicitColor) return explicitColor;

  const entryColor = String(entries[0]?._nodeColor || "").trim();
  if (entryColor) return entryColor;

  try {
    const nodes = getNodes();
    const current = nodes.find((node) => node.id === String(rootId || "").trim()) || nodes[0];
    return current?.color || DEFAULT_NODE_COLOR;
  } catch {
    return DEFAULT_NODE_COLOR;
  }
}
