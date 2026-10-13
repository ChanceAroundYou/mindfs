export function estimateCommandTerminalCols(): number | undefined {
  if (typeof window === "undefined") return undefined;
  const width = window.visualViewport?.width || window.innerWidth || 0;
  if (width <= 0) return undefined;
  return Math.max(40, Math.min(500, Math.floor(Math.max(280, width - (width < 768 ? 92 : 72)) / 7.25) - 1));
}
