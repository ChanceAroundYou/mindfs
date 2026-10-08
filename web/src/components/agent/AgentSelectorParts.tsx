import React from "react";
import { createPortal } from "react-dom";
import type { AgentStatus } from "../../services/agents";

export function AgentMenuPortal({
  enabled,
  children,
}: {
  enabled: boolean;
  children: React.ReactNode;
}) {
  if (enabled && typeof document !== "undefined") {
    return createPortal(children, document.body);
  }
  return <>{children}</>;
}

export const AGENT_MODEL_SEARCH_THRESHOLD = 8;

export function strip1MSuffix(model: string): string {
  const trimmed = String(model || "").trim();
  return trimmed.toLowerCase().endsWith("[1m]") ? trimmed.slice(0, -4).trim() : trimmed;
}

export function isClaudeAliasModelName(model: string): boolean {
  const base = strip1MSuffix(model).trim().toLowerCase();
  return base === "fable" || base === "opus" || base === "sonnet" || base === "haiku" || base === "of" || base === "op" || base === "os" || base === "ok" || base === "default";
}

export function claudeModelBase(model: string): string {
  if (!isClaudeAliasModelName(model)) return strip1MSuffix(model);
  const base = strip1MSuffix(model);
  switch (base.toLowerCase()) {
    case "fable": return "of";
    case "opus": return "op";
    case "sonnet": return "os";
    case "haiku": return "ok";
    case "of": case "op": case "os": case "ok": return base.toLowerCase();
    default: return base;
  }
}

export function hasAgentOptions(agent?: AgentStatus): boolean {
  const isClaude = String(agent?.name || "").trim().toLowerCase() === "claude";
  return !!(
    agent &&
    ((agent.models?.length ?? 0) > 0 ||
      (agent.modes?.length ?? 0) > 0 ||
      (agent.efforts?.length ?? 0) > 0 ||
      agent.supports_fast_service ||
      isClaude)
  );
}

export function parseAgentErrorMessage(error?: string): string {
  const raw = String(error || "").trim();
  if (!raw) {
    return "";
  }

  try {
    const parsed = JSON.parse(raw) as {
      message?: unknown;
    };
    return typeof parsed.message === "string" && parsed.message.trim()
      ? parsed.message.trim()
      : raw;
  } catch {
    return raw;
  }
}

export function parseAgentErrorDetails(error?: string): string[] {
  const raw = String(error || "").trim();
  if (!raw) {
    return [];
  }

  try {
    const parsed = JSON.parse(raw) as {
      data?: unknown;
    };
    if (parsed.data === undefined) {
      return [];
    }

    if (Array.isArray(parsed.data)) {
      return parsed.data.map((item) => String(item)).filter(Boolean);
    }

    if (parsed.data && typeof parsed.data === "object") {
      if (
        Array.isArray((parsed.data as { authMethods?: unknown }).authMethods)
      ) {
        return (
          parsed.data as {
            authMethods: Array<{ name?: unknown; description?: unknown }>;
          }
        ).authMethods
          .map((item) => {
            const name = typeof item?.name === "string" ? item.name.trim() : "";
            const description =
              typeof item?.description === "string"
                ? item.description.trim()
                : "";
            if (name && description) {
              return `${name}: ${description}`;
            }
            return name || description;
          })
          .filter(Boolean);
      }
      return Object.entries(parsed.data as Record<string, unknown>).map(
        ([key, value]) => {
          if (typeof value === "string") {
            return `${key}: ${value}`;
          }
          return `${key}: ${JSON.stringify(value)}`;
        },
      );
    }

    return [String(parsed.data)];
  } catch {
    return [];
  }
}

export function SectionHeader({
  title,
  expanded,
  onToggle,
  topBorder = false,
  value,
}: {
  title: string;
  expanded: boolean;
  onToggle: () => void;
  topBorder?: boolean;
  value?: string;
}) {
  return (
    <button
      type="button"
      onClick={onToggle}
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        width: "100%",
        minWidth: 0,
        padding: "10px 12px",
        border: "none",
        borderTop: topBorder ? "1px solid var(--menu-divider)" : "none",
        background: expanded ? "var(--selection-bg)" : "transparent",
        color: "var(--text-primary)",
        textAlign: "left",
        cursor: "pointer",
      }}
    >
      <span
        style={{
          flex: "0 0 auto",
          fontSize: "11px",
          fontWeight: 700,
          letterSpacing: "0.08em",
          textTransform: "uppercase",
          color: expanded ? "var(--accent-color)" : "var(--text-secondary)",
          whiteSpace: "nowrap",
        }}
      >
        {title}
      </span>
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "flex-end",
          gap: "8px",
          flex: "1 1 auto",
          minWidth: 0,
          marginLeft: "8px",
        }}
      >
        {value ? (
          <span
            title={value}
            style={{
              minWidth: 0,
              fontSize: "11px",
              color: "var(--text-secondary)",
              whiteSpace: "nowrap",
              maxWidth: "92px",
              overflow: "hidden",
              textOverflow: "ellipsis",
              direction: "rtl",
              textAlign: "left",
            }}
          >
            {value}
          </span>
        ) : null}
        <SelectorChevron expanded={expanded} />
      </span>
    </button>
  );
}

export function SelectorChevron({ expanded }: { expanded: boolean }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 12 12"
      fill="none"
      aria-hidden="true"
      style={{
        flexShrink: 0,
        color: expanded ? "var(--accent-color)" : "var(--text-secondary)",
        transform: expanded ? "rotate(90deg)" : "rotate(0deg)",
        transition: "transform 0.16s ease",
      }}
    >
      <path
        d="M4 2.5 8 6 4 9.5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function sectionItemStyle(
  selected: boolean,
  topBorder = false,
  opacity = 1,
): React.CSSProperties {
  return {
    display: "flex",
    flexDirection: "column",
    alignItems: "flex-start",
    gap: "2px",
    width: "100%",
    minWidth: 0,
    padding: "10px 12px",
    border: "none",
    borderTop: topBorder ? "1px solid var(--menu-divider)" : "none",
    background: selected ? "var(--selection-bg)" : "transparent",
    color: selected ? "var(--accent-color)" : "var(--text-primary)",
    textAlign: "left",
    cursor: "pointer",
    opacity,
  };
}

export function ModelMenuItem({
  item,
  selected,
  topBorder,
  onSelect,
  agentName,
}: {
  item: NonNullable<AgentStatus["models"]>[number];
  selected: boolean;
  topBorder: boolean;
  onSelect: (agent: string, model?: string) => void;
  agentName: string;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect(agentName, item.id)}
      style={sectionItemStyle(selected, topBorder, item.hidden ? 0.66 : 1)}
      title={item.description || item.id}
    >
      <span style={{ fontSize: "13px", fontWeight: 500 }}>
        {item.name || item.id}
      </span>
      {item.description ? (
        <span
          style={{
            fontSize: "11px",
            color: "var(--text-secondary)",
            whiteSpace: "normal",
            overflowWrap: "anywhere",
            wordBreak: "break-word",
          }}
        >
          {item.description}
        </span>
      ) : item.hidden ? (
        <span
          style={{
            fontSize: "11px",
            color: "var(--text-secondary)",
          }}
        >
          hidden
        </span>
      ) : null}
    </button>
  );
}
