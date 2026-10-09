import React from "react";

export function renderStatusColor(status: string): string {
  switch (status) {
    case "A":
      return "#15803d";
    case "D":
      return "#b91c1c";
    case "R":
      return "#1d4ed8";
    case "??":
      return "#7c3aed";
    default:
      return "#b45309";
  }
}

export function renderLineStat(value: number, prefix: "+" | "-"): React.ReactNode {
  const color = prefix === "+" ? "#15803d" : "#b91c1c";
  return (
    <span style={{ color, fontVariantNumeric: "tabular-nums" }}>
      {prefix}{value}
    </span>
  );
}

export function renderStatusLabel(status: string): string {
  if (status === "??") {
    return "U";
  }
  return status;
}

export function compactPath(path: string): string {
  const normalized = String(path || "").replace(/\\/g, "/").replace(/\/+$/g, "");
  return normalized.split("/").filter(Boolean).pop() || normalized || path;
}
