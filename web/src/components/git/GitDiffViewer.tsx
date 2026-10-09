import React from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import {
  getInlineDiffSegments,
  type DiffLine,
} from "../../shared/gitDiffModel";
import { useGitDiffViewer, type GitDiffViewerProps } from "./useGitDiffViewer";

function Breadcrumbs({ root, rootDisplayName, path, rootColor, onPathClick }: { root?: string | null; rootDisplayName?: string | null; path: string; rootColor?: string | null; onPathClick?: (path: string) => void }) {
  const parts = path.split('/').filter(Boolean);
  const getPathAt = (index: number) => parts.slice(0, index + 1).join('/');

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '13px', color: 'var(--text-secondary)', overflow: 'hidden', whiteSpace: 'nowrap', flexShrink: 1, justifyContent: 'flex-start' }}>
      {root && (
        <>
          <button
            type="button"
            data-onboarding="project-home"
            onClick={() => onPathClick?.(".")}
            style={{
              ...rootBadgeButtonStyle,
              background: "var(--node-badge-bg)",
              color: String(rootColor || "").trim() || "var(--text-primary)",
              cursor: "pointer",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
            onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}
          >
            {rootDisplayName || root}
          </button>
          {parts.length > 0 && <span style={{ opacity: 0.4, fontSize: '10px', flexShrink: 0 }}>❯</span>}
        </>
      )}
      {parts.map((part, index) => (
        <React.Fragment key={index}>
          <span
            onClick={() => index < parts.length - 1 && onPathClick?.(getPathAt(index))}
            style={{ fontWeight: index === parts.length - 1 ? 600 : 400, color: index === parts.length - 1 ? 'var(--text-primary)' : 'inherit', cursor: index < parts.length - 1 ? 'pointer' : 'default', overflow: 'hidden', textOverflow: 'ellipsis' }}
            onMouseEnter={(e) => { if (index < parts.length - 1) e.currentTarget.style.textDecoration = 'underline'; }}
            onMouseLeave={(e) => { e.currentTarget.style.textDecoration = 'none'; }}
          >
            {part}
          </span>
          {index < parts.length - 1 && <span style={{ opacity: 0.4, fontSize: '10px', flexShrink: 0 }}>❯</span>}
        </React.Fragment>
      ))}
    </div>
  );
}

function renderLineStat(value: number, prefix: "+" | "-"): React.ReactNode {
  if (!value) return null;
  return (
    <span style={{ color: prefix === "+" ? "#16a34a" : "#dc2626" }}>
      {prefix}{value}
    </span>
  );
}

function renderStatusLabel(status: string): string {
  switch (status) {
    case "added": return "新增";
    case "modified": return "修改";
    case "deleted": return "删除";
    case "renamed": return "重命名";
    default: return status;
  }
}

function lineBackground(kind: DiffLine["kind"]): string {
  switch (kind) {
    case "add": return "rgba(34,197,94,0.08)";
    case "del": return "rgba(239,68,68,0.08)";
    case "hunk": return "rgba(148,163,184,0.12)";
    default: return "transparent";
  }
}

function lineColor(kind: DiffLine["kind"]): string {
  switch (kind) {
    case "add": return "#15803d";
    case "del": return "#b91c1c";
    case "hunk": return "#64748b";
    default: return "var(--text-primary)";
  }
}

function displayLineNumber(line: DiffLine): string {
  return String(line.newLine ?? "");
}

function displayOldLineNumber(line?: DiffLine): string {
  return line ? String(line.oldLine ?? "") : "";
}

function displayNewLineNumber(line?: DiffLine): string {
  return line ? String(line.newLine ?? "") : "";
}

function inlineSegmentBackground(kind: DiffLine["kind"], segmentKind: "ctx" | "add" | "del"): string {
  if (segmentKind === "add") return kind === "add" ? "rgba(34,197,94,0.2)" : "transparent";
  if (segmentKind === "del") return kind === "del" ? "rgba(239,68,68,0.2)" : "transparent";
  return "transparent";
}

function renderDiffLineContent(line: DiffLine, counterpart?: DiffLine, includePrefix = true): React.ReactNode {
  const prefix = line.kind === "add" ? "+" : line.kind === "del" ? "-" : " ";
  if (!counterpart || line.kind === "ctx" || counterpart.kind === "ctx") {
    return `${includePrefix ? prefix : ""}${line.text || " "}`;
  }

  const oldText = line.kind === "del" ? line.text : counterpart.text;
  const newText = line.kind === "add" ? line.text : counterpart.text;
  const hiddenKind = line.kind === "add" ? "del" : "add";
  const visibleSegments = getInlineDiffSegments(oldText, newText).filter((segment) => segment.kind !== hiddenKind);

  return (
    <>
      {includePrefix ? <span>{prefix}</span> : null}
      {visibleSegments.length > 0
        ? visibleSegments.map((segment, index) => (
          <span
            key={`${index}-${segment.kind}-${segment.text}`}
            style={{
              background: inlineSegmentBackground(line.kind, segment.kind),
              borderRadius: segment.kind === "ctx" ? 0 : "3px",
            }}
          >
            {segment.text}
          </span>
        ))
        : <span>{line.text || " "}</span>}
    </>
  );
}

export function GitDiffViewer(props: GitDiffViewerProps) {
  const {
    sideBySideRows,
    unifiedRows,
    relatedSessions,
    displayPath,
    contentRootRef,
    isMobile,
    visibleRelatedSessions,
  } = useGitDiffViewer(props);

  const { diff, root, rootDisplayName, rootColor, sideBySide = false, onPathClick, onSessionClick } = props;

  return (
    <div style={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0 }}>
      <ViewHeader padding="0 16px" style={{ gap: "10px" }} innerStyle={{ gap: "10px" }}>
        <div style={{ display: "flex", alignItems: "center", overflow: "hidden", flex: 1, minWidth: 0 }}>
          <Breadcrumbs root={root} rootDisplayName={rootDisplayName} path={displayPath} rootColor={rootColor} onPathClick={onPathClick} />

          {relatedSessions.length > 0 ? (
            <div style={{ marginLeft: "16px", display: "flex", alignItems: "center", gap: "6px", minWidth: 0, flexShrink: 0 }}>
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                style={{ color: "var(--text-secondary)", opacity: 0.4 }}
              >
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
              </svg>
              <div style={{ display: "flex", gap: "4px", overflowX: "auto", whiteSpace: "nowrap", scrollbarWidth: "none" }}>
                {visibleRelatedSessions.map((meta) => (
                  <button
                    key={meta.source_session}
                    type="button"
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      if (meta.source_session) {
                        onSessionClick?.(meta.source_session);
                      }
                    }}
                    style={{
                      background: "rgba(0, 0, 0, 0.03)",
                      border: "1px solid rgba(0, 0, 0, 0.05)",
                      borderRadius: "6px",
                      padding: "1px 8px",
                      cursor: "pointer",
                      color: "var(--text-secondary)",
                      fontSize: "11px",
                      fontWeight: 500,
                      flexShrink: 0,
                      transition: "all 0.2s ease",
                    }}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.background = "var(--selection-bg)";
                      e.currentTarget.style.color = "var(--accent-color)";
                      e.currentTarget.style.borderColor = "color-mix(in srgb, var(--accent-color) 20%, transparent)";
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.background = "rgba(0, 0, 0, 0.03)";
                      e.currentTarget.style.color = "var(--text-secondary)";
                      e.currentTarget.style.borderColor = "rgba(0, 0, 0, 0.05)";
                    }}
                  >
                    <span
                      style={{
                        display: "inline-block",
                        maxWidth: isMobile ? "72px" : "120px",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                        verticalAlign: "bottom",
                      }}
                      title={meta.session_name || `Session ${meta.source_session.slice(0, 8)}`}
                    >
                      {meta.session_name || `Session ${meta.source_session.slice(0, 8)}`}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
          <div style={{ marginLeft: "auto", display: "inline-flex", alignItems: "center", gap: "8px", fontSize: "11px", color: "var(--text-secondary)", minWidth: 0, flexShrink: 0 }}>
            <span>{renderStatusLabel(diff.status)}</span>
            {renderLineStat(diff.additions, "+")}
            {renderLineStat(diff.deletions, "-")}
          </div>
        </div>
      </ViewHeader>

      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <div
          style={{
            fontFamily: 'Menlo, Monaco, "Courier New", monospace',
            fontSize: "13px",
            lineHeight: "20px",
            color: "var(--text-primary)",
            background: "transparent",
          }}
        >
          <div ref={contentRootRef} style={{ position: "relative" }}>
            <div style={{ padding: "24px 8px 24px 4px" }}>
              <div style={{ color: "var(--text-primary)", minWidth: sideBySide ? "960px" : 0 }}>
                {sideBySide ? (
                  sideBySideRows.map((row, index) => {
                    if (row.kind === "hunk") {
                      return (
                        <div
                          key={`${index}-hunk`}
                          style={{
                            display: "grid",
                            gridTemplateColumns: "minmax(0, 1fr)",
                            background: lineBackground("hunk"),
                            color: lineColor("hunk"),
                          }}
                        >
                          <div style={{ padding: "0 12px", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                            {row.hunkText || " "}
                          </div>
                        </div>
                      );
                    }
                    const leftKind = row.left?.kind || "ctx";
                    const rightKind = row.right?.kind || "ctx";
                    return (
                      <div
                        key={`${index}-${row.left?.oldLine || 0}-${row.right?.newLine || 0}`}
                        data-line-number={typeof row.right?.newLine === "number" ? row.right.newLine : undefined}
                        style={{
                          display: "grid",
                          gridTemplateColumns: "48px minmax(0, 1fr) 48px minmax(0, 1fr)",
                          alignItems: "stretch",
                          borderBottom: "1px solid rgba(148, 163, 184, 0.08)",
                        }}
                      >
                        <div
                          style={{
                            padding: "0 6px 0 0",
                            textAlign: "right",
                            color: "var(--text-secondary)",
                            opacity: 0.55,
                            userSelect: "none",
                            fontVariantNumeric: "tabular-nums",
                            background: row.left ? lineBackground(leftKind) : "rgba(148, 163, 184, 0.05)",
                          }}
                        >
                          {displayOldLineNumber(row.left)}
                        </div>
                        <div
                          style={{
                            padding: "0 12px 0 6px",
                            whiteSpace: "pre-wrap",
                            wordBreak: "break-word",
                            background: row.left ? lineBackground(leftKind) : "rgba(148, 163, 184, 0.05)",
                            color: row.left ? lineColor(leftKind) : "var(--text-secondary)",
                            borderRight: "1px solid var(--border-color)",
                          }}
                        >
                          {row.left ? renderDiffLineContent(row.left, row.right) : " "}
                        </div>
                        <div
                          style={{
                            padding: "0 6px 0 0",
                            textAlign: "right",
                            color: "var(--text-secondary)",
                            opacity: 0.55,
                            userSelect: "none",
                            fontVariantNumeric: "tabular-nums",
                            background: row.right ? lineBackground(rightKind) : "rgba(148, 163, 184, 0.05)",
                          }}
                        >
                          {displayNewLineNumber(row.right)}
                        </div>
                        <div
                          style={{
                            padding: "0 12px 0 6px",
                            whiteSpace: "pre-wrap",
                            wordBreak: "break-word",
                            background: row.right ? lineBackground(rightKind) : "rgba(148, 163, 184, 0.05)",
                            color: row.right ? lineColor(rightKind) : "var(--text-secondary)",
                          }}
                        >
                          {row.right ? renderDiffLineContent(row.right, row.left) : " "}
                        </div>
                      </div>
                    );
                  })
                ) : (
                  unifiedRows.map((row, index) => {
                    if (row.kind === "hunk") {
                      return (
                        <div
                          key={`${index}-hunk`}
                          style={{
                            display: "grid",
                            gridTemplateColumns: "minmax(0, 1fr)",
                            background: lineBackground("hunk"),
                            color: lineColor("hunk"),
                          }}
                        >
                          <div style={{ padding: "0 12px", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                            {row.hunkText || " "}
                          </div>
                        </div>
                      );
                    }
                    const line = row.line;
                    return (
                      <div
                        key={`${index}-${line.kind}-${line.oldLine || 0}-${line.newLine || 0}`}
                        data-line-number={typeof line.newLine === "number" ? line.newLine : undefined}
                        style={{
                          display: "grid",
                          gridTemplateColumns: "34px 14px minmax(0, 1fr)",
                          alignItems: "stretch",
                          background: lineBackground(line.kind),
                          color: lineColor(line.kind),
                        }}
                      >
                        <div
                          style={{
                            padding: "0 4px 0 0",
                            textAlign: "right",
                            color: "var(--text-secondary)",
                            opacity: 0.55,
                            userSelect: "none",
                            fontVariantNumeric: "tabular-nums",
                          }}
                        >
                          {displayLineNumber(line)}
                        </div>
                        <div style={{ padding: "0", userSelect: "none", fontWeight: 700 }}>
                          {line.kind === "add" ? "+" : line.kind === "del" ? "-" : line.kind === "ctx" ? " " : ""}
                        </div>
                        <div style={{ padding: "0 12px 0 4px", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                          {renderDiffLineContent(line, row.counterpart, false)}
                        </div>
                      </div>
                    );
                  })
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
