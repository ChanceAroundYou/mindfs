import { rootBadgeButtonStyle, rootBadgeStyle } from "./rootBadgeStyle";

function badgeBg(_color: string): string {
  return "var(--node-badge-bg)";
}

export function NodeBadgeHeader({
  color,
  label,
  collapsed = false,
  onClick,
}: {
  color: string;
  label: string;
  collapsed?: boolean;
  onClick?: () => void;
}) {
  const c = String(color || "#6d5bcf").trim() || "#6d5bcf";
  return (
    <div
      style={{
        minWidth: 0,
        height: "22px",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        gap: "8px",
        padding: "0 24px 0 2px",
        background: "transparent",
        boxSizing: "border-box",
        position: "relative",
      }}
    >
      <span aria-hidden="true" style={{ height: "1px", flex: 1, minWidth: "12px", background: "var(--border-color)" }} />
      {onClick ? (
        <button
          type="button"
          onClick={onClick}
          style={{
            ...rootBadgeButtonStyle,
            background: badgeBg(c),
            flexShrink: 1,
            maxWidth: "100%",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            cursor: "pointer",
            display: "inline-flex",
            alignItems: "center",
            gap: "5px",
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            style={{
              flexShrink: 0,
              transform: collapsed ? "rotate(0deg)" : "rotate(90deg)",
              transition: "transform 0.15s cubic-bezier(0.4, 0, 0.2, 1)",
              color: c,
            }}
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
          <span style={{ color: c, fontWeight: 600 }}>{label}</span>
        </button>
      ) : (
        <span
          style={{
            ...rootBadgeStyle,
            background: badgeBg(c),
            flexShrink: 1,
            maxWidth: "100%",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          <span style={{ color: c, fontWeight: 600 }}>{label}</span>
        </span>
      )}
      <span aria-hidden="true" style={{ height: "1px", flex: 1, minWidth: "12px", background: "var(--border-color)" }} />
    </div>
  );
}
