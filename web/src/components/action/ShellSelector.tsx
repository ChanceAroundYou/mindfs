import React, { useEffect, useRef, useState } from "react";
import { type ShellStatus } from "../../services/agents";

export function ShellSelector({
  shell,
  shells,
  onShellChange,
  compact = false,
}: {
  shell: string;
  shells: ShellStatus[];
  onShellChange: (shell: string) => void;
  compact?: boolean;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const selected = shells.find((item) => item.id === shell || item.command === shell || item.resolved_command === shell) || shells.find((item) => item.default) || shells[0];

  useEffect(() => {
    const handlePointerOutside = (event: PointerEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };
    if (isOpen) {
      document.addEventListener("pointerdown", handlePointerOutside);
      return () => document.removeEventListener("pointerdown", handlePointerOutside);
    }
  }, [isOpen]);

  return (
    <div ref={dropdownRef} style={{ position: "relative" }}>
      <button
        type="button"
        onClick={() => shells.length > 0 && setIsOpen((prev) => !prev)}
        disabled={shells.length === 0}
        title="Shell"
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          maxWidth: compact ? "72px" : "92px",
          height: compact ? "24px" : "28px",
          border: "none",
          borderRadius: "8px",
          background: "transparent",
          color: "inherit",
          fontSize: "11px",
          fontWeight: 700,
          lineHeight: 1,
          padding: "0 3px",
          outline: "none",
          cursor: shells.length === 0 ? "default" : "pointer",
          opacity: shells.length === 0 ? 0.45 : 1,
        }}
      >
        <span
          style={{
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            display: "inline-block",
            maxWidth: "100%",
            padding: "1px 4px",
            borderRadius: "6px",
            background: "#1d4ed8",
            color: "#fff",
            lineHeight: 1.2,
            boxSizing: "border-box",
          }}
        >
          {selected?.label || "shell"}
        </span>
      </button>

      {isOpen && (
        <div
          style={{
            position: "absolute",
            bottom: "calc(100% + 8px)",
            right: 0,
            background: "var(--menu-bg)",
            border: "1px solid var(--menu-border)",
            borderRadius: "12px",
            boxShadow: "0 8px 32px rgba(0,0,0,0.15)",
            zIndex: 1000,
            width: "max-content",
            minWidth: "104px",
            maxWidth: "min(72vw, 180px)",
            padding: "8px 0",
          }}
        >
          <div
            style={{
              padding: "6px 12px",
              fontSize: "11px",
              fontWeight: 600,
              color: "var(--text-secondary)",
              textTransform: "uppercase",
            }}
          >
            Shell
          </div>
          {shells.map((item) => {
            const isSelected = item.id === selected?.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => {
                  onShellChange(item.id);
                  setIsOpen(false);
                }}
                style={{
                  display: "block",
                  width: "100%",
                  padding: "9px 12px",
                  border: "none",
                  background: isSelected ? "var(--selection-bg)" : "transparent",
                  color: isSelected ? "var(--accent-color)" : "var(--text-primary)",
                  fontSize: "13px",
                  fontWeight: isSelected ? 700 : 500,
                  textAlign: "left",
                  cursor: "pointer",
                  whiteSpace: "nowrap",
                }}
                onMouseEnter={(event) => {
                  if (!isSelected) {
                    event.currentTarget.style.background = "rgba(0,0,0,0.04)";
                  }
                }}
                onMouseLeave={(event) => {
                  event.currentTarget.style.background = isSelected ? "var(--selection-bg)" : "transparent";
                }}
              >
                {item.label}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
