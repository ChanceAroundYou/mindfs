import React from "react";

export type AsyncStateMessageKind = "loading" | "empty" | "error";

export type AsyncStateMessageProps = {
  kind: AsyncStateMessageKind | null;
  children: React.ReactNode;
  style?: React.CSSProperties;
};

const baseStyle: React.CSSProperties = {
  fontSize: "12px",
  color: "var(--text-secondary)",
};

/** Shared inline message for asynchronous loading, empty, and error states. */
export function AsyncStateMessage({ kind, children, style }: AsyncStateMessageProps) {
  if (kind === null) return null;

  return (
    <div
      role={kind === "loading" ? "status" : kind === "error" ? "alert" : undefined}
      style={{
        ...baseStyle,
        ...(kind === "error" ? { color: "#b45309" } : {}),
        ...style,
      }}
    >
      {children}
    </div>
  );
}
