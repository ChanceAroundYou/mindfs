import React from "react";
import { MarkdownViewer } from "../common/MarkdownViewer";
import type { TodoUpdate } from "../../shared/sessionTypes";

export function PlanUpdateCard({ content, rootId }: { content: string; rootId?: string | null }) {
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid color-mix(in srgb, var(--accent-color) 24%, transparent)",
        background: "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 8%, transparent), color-mix(in srgb, var(--accent-color) 3%, transparent))",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "6px 8px",
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          borderBottom: "1px solid var(--border-color)",
        }}
      >
        Plan
      </div>
      <div style={{ padding: "10px" }}>
        <MarkdownViewer content={content || ""} root={rootId || undefined} />
      </div>
    </div>
  );
}

export function CompactNoticeCard({
  status,
  summary,
}: {
  status?: string;
  summary?: string;
}) {
  const normalizedStatus = `${status || ""}`.toLowerCase();
  const label =
    normalizedStatus === "running"
      ? "Compacting context"
      : normalizedStatus === "error"
        ? "Context compaction failed"
        : "Context compacted";
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid rgba(148, 163, 184, 0.28)",
        background: "rgba(148, 163, 184, 0.08)",
        padding: "10px",
        color: "var(--text-secondary)",
        fontSize: "13px",
      }}
    >
      <div style={{ fontWeight: 600, color: "var(--text-primary)" }}>{label}</div>
      {summary ? <div style={{ marginTop: "6px" }}>{summary}</div> : null}
    </div>
  );
}

export function TodoUpdateCard({ todoUpdate }: { todoUpdate: TodoUpdate }) {
  const items = Array.isArray(todoUpdate?.items) ? todoUpdate.items : [];
  return (
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: "1px solid rgba(16, 185, 129, 0.22)",
        background:
          "linear-gradient(180deg, rgba(16, 185, 129, 0.08), rgba(16, 185, 129, 0.03))",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "6px 8px",
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          borderBottom: "1px solid var(--border-color)",
        }}
      >
        ✅ todos
      </div>
      <div
        style={{
          padding: "10px",
          display: "flex",
          flexDirection: "column",
          gap: "6px",
        }}
      >
        {items.map((item, index) => {
          const status = `${item?.status || ""}`.trim().toLowerCase();
          const content = `${item?.content || ""}`.trim();
          const activeForm = `${item?.activeForm || ""}`.trim();
          const label =
            status === "in_progress" && activeForm
              ? activeForm
              : content || activeForm;
          const checked = status === "completed";
          return (
            <div
              key={`${index}-${label}`}
              style={{
                fontSize: "13px",
                color: "var(--text-primary)",
                opacity: checked ? 0.78 : 1,
                textDecoration: checked ? "line-through" : "none",
              }}
            >
              {checked ? "☑" : "☐"} {label}
              {status === "in_progress" ? " (in progress)" : ""}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function UserMessageListIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 32 32" aria-hidden="true">
      <path d="M0 0h32v32H0z" fill="none" />
      <path fill="currentColor" d="M4.082 4.083v3h22.835v-3zm0 16.223h22.835v-3H4.082zm0-6.612h22.835v-3H4.082zm0 13.223h22.835v-3H4.082z" />
    </svg>
  );
}
