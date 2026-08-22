export const BOTTOM_SHEET_DRAG_START_PX = 8;
export const BOTTOM_SHEET_TOP_EDGE_RATIO = 0.1;
export const BOTTOM_SHEET_BOTTOM_EDGE_RATIO = 0.2;
// compat alias for existing tests/imports
export const BOTTOM_SHEET_EDGE_RATIO = BOTTOM_SHEET_BOTTOM_EDGE_RATIO;

export type BottomSheetRelease = "half" | "close" | "expand";

export function resolveBottomSheetRelease({
  clientY,
  viewportHeight,
  dragged,
}: {
  clientY: number;
  viewportHeight: number;
  dragged: boolean;
}): BottomSheetRelease {
  if (!dragged || viewportHeight <= 0) return "half";
  if (clientY <= viewportHeight * BOTTOM_SHEET_TOP_EDGE_RATIO) return "expand";
  if (clientY >= viewportHeight * (1 - BOTTOM_SHEET_BOTTOM_EDGE_RATIO)) return "close";
  return "half";
}
