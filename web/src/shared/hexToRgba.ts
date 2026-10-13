import { DEFAULT_NODE_COLOR } from "./nodePalette";

const DEFAULT_NODE_RGB = (() => {
  const hex = DEFAULT_NODE_COLOR.replace(/^#/, "");
  return [0, 2, 4].map((index) => parseInt(hex.slice(index, index + 2), 16));
})();

export function hexToRgba(hex: string, alpha: number): string {
  const value = String(hex || "").trim().replace(/^#/, "");
  const fallback = `rgba(${DEFAULT_NODE_RGB.join(", ")}, ${alpha})`;

  if (value.length === 3) {
    const red = parseInt(value[0] + value[0], 16);
    const green = parseInt(value[1] + value[1], 16);
    const blue = parseInt(value[2] + value[2], 16);
    if (Number.isFinite(red) && Number.isFinite(green) && Number.isFinite(blue)) {
      return `rgba(${red}, ${green}, ${blue}, ${alpha})`;
    }
    return fallback;
  }

  if (value.length === 6) {
    const red = parseInt(value.slice(0, 2), 16);
    const green = parseInt(value.slice(2, 4), 16);
    const blue = parseInt(value.slice(4, 6), 16);
    if (Number.isFinite(red) && Number.isFinite(green) && Number.isFinite(blue)) {
      return `rgba(${red}, ${green}, ${blue}, ${alpha})`;
    }
  }

  if (/^rgba?\(/.test(String(hex || ""))) return String(hex);
  return fallback;
}
