import React, { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { sessionService, type ToolCall, type ToolCallContentItem, type ToolCallLocation } from "../../services/session";
import { MarkdownViewer } from "../file/MarkdownViewer";
import { useI18n } from "../../i18n";
import { AnsiOutput, statusColors, terminalFontFamily } from "./toolCallRender";
import { renderToolIcon } from "./toolCallIcons";
import { buildDetailSections, normalizeDisplayPath, isDiffLikeText, extractDiffPath, type DetailSection } from "./toolCallSections";
import { basename, shouldPreserveDisplayStatus, isRunningStatus, stringMeta, toolProgressText, extractExecuteCommand, ExecuteToolDetails, CollabToolDetails } from "./toolCallHelpers";

type ToolCallCardProps = {
  kind?: string;
  title?: string;
  callId: string;
  status: string;
  content?: ToolCallContentItem[];
  result?: string;
  locations?: ToolCallLocation[];
  meta?: Record<string, unknown>;
  rootPath?: string;
  rootId?: string | null;
  sessionKey?: string | null;
  defaultExpanded?: boolean;
  viewCache?: ToolCallViewCache;
  viewCacheKey?: string;
  getFallbackResult?: (toolCall: Partial<ToolCall>) => string;
};

export type ToolCallViewCache = {
  expanded: Map<string, boolean>;
  details: Map<string, { source: unknown; content: unknown; status: string; tool: ToolCall }>;
};

export const ToolCallCard = memo(function ToolCallCard({
  kind,
  title,
  callId: _callId,
  status,
  content,
  result: suppliedResult,
  locations,
  meta,
  rootPath,
  rootId,
  sessionKey,
  defaultExpanded = false,
  viewCache,
  viewCacheKey,
  getFallbackResult,
}: ToolCallCardProps) {
  const { t } = useI18n();
  const expansionKey = viewCacheKey || _callId;
  const [expanded, setExpandedState] = useState(() => viewCache?.expanded.get(expansionKey) ?? defaultExpanded);
  const setExpanded = (value: boolean) => {
    viewCache?.expanded.set(expansionKey, value);
    setExpandedState(value);
  };
  const result = useMemo(
    () => expanded ? (getFallbackResult?.({ kind, meta }) ?? suppliedResult) : undefined,
    [expanded, getFallbackResult, kind, meta, suppliedResult],
  );
  const [loadedToolCall, setLoadedToolCall] = useState<ToolCall | null>(null);
  const [loadingDetails, setLoadingDetails] = useState(false);
  const [detailLoadFailed, setDetailLoadFailed] = useState(false);
  const detailScrollRef = useRef<HTMLDivElement | null>(null);
  const loadingDetailKeyRef = useRef("");
  const loadedDetailKeyRef = useRef("");
  const detailGenerationRef = useRef(0);
  const shouldStickDetailsToBottomRef = useRef(true);
  const effectiveKind = loadedToolCall?.kind || kind;
  const effectiveTitle = loadedToolCall?.title || title;
  const effectiveStatus = isRunningStatus(status)
    ? status
    : shouldPreserveDisplayStatus(status, loadedToolCall?.status)
    ? status
    : loadedToolCall?.status || status;
  const effectiveContent = loadedToolCall?.content || content;
  const effectiveLocations = loadedToolCall?.locations || locations;
  const effectiveMeta =
    loadedToolCall?.meta || meta
      ? { ...(loadedToolCall?.meta || {}), ...(meta || {}) }
      : undefined;
  const labelKind = (effectiveKind || "").trim();
  const labelTitle = (effectiveTitle || "").trim();
  const normalizedKind = labelKind.toLowerCase();
  const isCollabTool = effectiveMeta?.rawType === "collabToolCall" || effectiveMeta?.type === "collabAgentToolCall";
  const collabToolName = typeof effectiveMeta?.tool === "string" ? effectiveMeta.tool.trim() : "";
  const isCollabWait = isCollabTool && collabToolName === "wait";
  const isExecute = normalizedKind === "execute";
  const isUserShell = isExecute && effectiveMeta?.source === "userShell";
  const executeCommand = isExecute ? extractExecuteCommand(effectiveMeta, labelTitle) : "";
  const userShellText = useMemo(
    () => expanded ? (effectiveContent || []).map((item) => ("text" in item ? item.text || "" : "")).join("") || result || "" : "",
    [expanded, effectiveContent, result],
  );
  const executeOutputText = useMemo(
    () => {
      if (!expanded) return "";
      const contentText = (effectiveContent || []).map((item) => ("text" in item ? item.text || "" : "")).join("");
      if (contentText) return contentText;
      if (!result) return "";
      const rawInput = stringMeta(effectiveMeta, "input");
      return result === rawInput || result === executeCommand ? "" : result;
    },
    [expanded, effectiveContent, effectiveMeta, executeCommand, result],
  );
  const hasContent = !!(effectiveContent && effectiveContent.length > 0);
  const hasLocations = !!(effectiveLocations && effectiveLocations.length > 0);
  const hasResult = !!suppliedResult || (!!getFallbackResult && Boolean(meta?.input || meta?.output));
  const hasExecuteCommand = executeCommand.length > 0;
  const hasExecuteOutput = executeOutputText.trim().length > 0;
  const hasUserShellOutput = userShellText.trim().length > 0;
  const hasCollabDetails = isCollabTool && !isCollabWait && Boolean(effectiveMeta?.prompt);
  const canLoadDetails = Boolean(rootId && sessionKey && _callId);
  const needsRemoteDetails = canLoadDetails && (isUserShell ? !hasUserShellOutput : isExecute ? !hasExecuteOutput : !hasContent);
  const hasDetails =
    (isUserShell
      ? hasUserShellOutput || hasContent
      : isExecute
      ? hasExecuteCommand || hasExecuteOutput || hasContent
      : hasContent || hasLocations || hasResult || hasCollabDetails) || canLoadDetails;
  const icon = renderToolIcon(normalizedKind);
  const normalizedStatus = (effectiveStatus || "").toLowerCase();
  const detailSections = useMemo(() => {
    if (!expanded) return [];
    const sections = buildDetailSections(effectiveContent, effectiveLocations, rootPath);
    if (sections.length > 0 || !result || !isDiffLikeText(result)) {
      return sections;
    }
    const fallbackPath = normalizeDisplayPath(effectiveLocations?.[0]?.path || "(unknown)", rootPath);
    return [
      {
        type: "diff" as const,
        path: normalizeDisplayPath(extractDiffPath(result, fallbackPath), rootPath),
        markdown: `~~~diff\n${result.trim()}\n~~~`,
      },
    ];
  }, [expanded, effectiveContent, effectiveLocations, result, rootPath]);
  const isFileChange =
    normalizedKind === "edit" ||
    normalizedKind === "delete" ||
    normalizedKind === "move" ||
    detailSections.some((section) => section.type === "diff");
  const fileNames = useMemo(() => {
    const diffNames = (effectiveContent || [])
      .map((item) => basename(normalizeDisplayPath(item.path || "", rootPath)))
      .filter(Boolean);
    const locationNames = (effectiveLocations || [])
      .map((loc) => basename(normalizeDisplayPath(loc.path, rootPath)))
      .filter(Boolean);
    return Array.from(new Set([...diffNames, ...locationNames]));
  }, [effectiveContent, effectiveLocations, rootPath]);
  const displayTitle = isFileChange && labelTitle.toLowerCase() === "file_change" ? "" : labelTitle;
  const displayFileNames = fileNames.filter((name) => name !== displayTitle && name !== basename(displayTitle));
  const label = isUserShell
    ? String(effectiveMeta?.command || displayTitle || "command")
    : isCollabTool
    ? displayTitle || collabToolName || "subagent"
    : isFileChange
    ? displayTitle
    : displayTitle || "tool";
  const isRunning = normalizedStatus === "running" || normalizedStatus === "in_progress";
  const isComplete = normalizedStatus === "complete" || normalizedStatus === "success";
  const isFailed = normalizedStatus === "failed" || normalizedStatus === "error";
  const progressText = toolProgressText(effectiveMeta);
  const showProgress = Boolean(progressText);
  const hasStructuredDetails = detailSections.length > 0;
  useEffect(() => {
    if (!hasDetails) {
      setExpanded(false);
      return;
    }
    if (defaultExpanded && !viewCache?.expanded.has(expansionKey)) {
      setExpanded(true);
    }
  }, [defaultExpanded, hasDetails]);

  useEffect(() => {
    const cached = viewCache?.details.get(_callId);
    const valid = cached?.source === meta && cached?.content === content && cached?.status === status;
    detailGenerationRef.current += 1;
    setLoadedToolCall(valid && cached ? cached.tool : null);
    setDetailLoadFailed(false);
    setLoadingDetails(false);
    loadingDetailKeyRef.current = "";
    loadedDetailKeyRef.current = valid ? `${rootId || ""}::${sessionKey || ""}::${_callId}` : "";
    return () => { detailGenerationRef.current += 1; };
  }, [_callId, rootId, sessionKey, meta, content, status, viewCache]);

  useEffect(() => {
    if (!expanded || !needsRemoteDetails) return;
    const detailKey = `${rootId || ""}::${sessionKey || ""}::${_callId}`;
    if (loadedDetailKeyRef.current === detailKey || loadingDetailKeyRef.current === detailKey) return;
    loadingDetailKeyRef.current = detailKey;
    const generation = detailGenerationRef.current;
    setLoadingDetails(true);
    setDetailLoadFailed(false);
    sessionService
      .getToolCall(String(rootId || ""), String(sessionKey || ""), _callId)
      .then((toolCall) => {
        if (generation !== detailGenerationRef.current || loadingDetailKeyRef.current !== detailKey) return;
        if (toolCall) {
          if (viewCache && !isRunningStatus(status)) {
            viewCache.details.delete(_callId);
            // Bound retained detail payloads; oversized results stay local to the mounted card.
            if (JSON.stringify(toolCall).length < 128 * 1024) {
              viewCache.details.set(_callId, { source: meta, content, status, tool: toolCall });
              while (viewCache.details.size > 32) {
                viewCache.details.delete(viewCache.details.keys().next().value!);
              }
            }
          }
          setLoadedToolCall(toolCall);
          loadedDetailKeyRef.current = detailKey;
        } else {
          setDetailLoadFailed(true);
        }
      })
      .catch(() => {
        if (generation === detailGenerationRef.current && loadingDetailKeyRef.current === detailKey) setDetailLoadFailed(true);
      })
      .finally(() => {
        if (generation !== detailGenerationRef.current || loadingDetailKeyRef.current !== detailKey) return;
        loadingDetailKeyRef.current = "";
        setLoadingDetails(false);
      });
  }, [_callId, expanded, needsRemoteDetails, rootId, sessionKey, meta, content, status, viewCache]);

  useEffect(() => {
    const container = detailScrollRef.current;
    if (!container || !isUserShell || !expanded) return;
    const updateStickiness = () => {
      const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight;
      shouldStickDetailsToBottomRef.current = distanceFromBottom < 48;
    };
    updateStickiness();
    container.addEventListener("scroll", updateStickiness, { passive: true });
    return () => container.removeEventListener("scroll", updateStickiness);
  }, [expanded, isUserShell]);

  const scrollUserShellDetailsToBottom = React.useCallback(() => {
    const container = detailScrollRef.current;
    if (!container || !shouldStickDetailsToBottomRef.current) return;
    container.scrollTop = container.scrollHeight;
  }, []);
  
  const statusColor = statusColors[normalizedStatus] || "#9ca3af";

  return (
    <>
    <div
      style={{
        width: "100%",
        minWidth: 0,
        borderRadius: "10px",
        border: isFileChange ? "1px solid color-mix(in srgb, var(--accent-color) 22%, transparent)" : "1px solid var(--border-color)",
        background: isFileChange
          ? "linear-gradient(180deg, color-mix(in srgb, var(--accent-color) 8%, transparent), color-mix(in srgb, var(--accent-color) 3%, transparent))"
          : "var(--content-bg)",
        boxShadow: isFileChange ? "inset 0 1px 0 rgba(255,255,255,0.35)" : "none",
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        onClick={hasDetails ? () => setExpanded(!expanded) : undefined}
        style={{
          width: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          padding: "6px 8px",
          background: isFileChange ? "color-mix(in srgb, var(--accent-color) 4%, transparent)" : "none",
          border: "none",
          cursor: hasDetails ? "pointer" : "default",
          fontSize: "12px",
          gap: "6px",
          minWidth: 0,
        }}
      >
        <span style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0, flex: 1 }}>
          <span>{icon}</span>
          {label ? (
            <span style={{ fontWeight: 500, color: "var(--text-primary)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", maxWidth: "100%", minWidth: 0 }}>
              {label}
            </span>
          ) : null}
          {isFileChange && displayFileNames.length > 0 ? (
            <span
              style={{
                minWidth: 0,
                padding: "1px 6px",
                borderRadius: "999px",
                background: "color-mix(in srgb, var(--accent-color) 10%, transparent)",
                color: "#1d4ed8",
                fontSize: "10px",
                fontWeight: 600,
                letterSpacing: "0.02em",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {displayFileNames.join(" ")}
            </span>
          ) : null}
        </span>
        <span
          style={{
            display: "flex",
            alignItems: "center",
            gap: "3px",
            flexShrink: 0,
            whiteSpace: "nowrap",
          }}
        >
          {isRunning && (
            <span
              style={{
                width: "6px",
                height: "6px",
                borderRadius: "50%",
                background: "#f59e0b",
                animation: "pulse 1s infinite",
              }}
            />
          )}
          {isFailed && (
            <span style={{ color: statusColor, fontSize: "12px", lineHeight: 1 }}>
              ✕
            </span>
          )}
        </span>
        {hasDetails && (
          <span
            style={{
              flexShrink: 0,
              transform: expanded ? "rotate(90deg)" : "rotate(0deg)",
              transition: "transform 0.2s",
              color: "var(--text-secondary)",
              display: "inline-flex",
              alignItems: "center",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="9 18 15 12 9 6" />
            </svg>
          </span>
        )}
      </button>

      {expanded && hasDetails && (
        <div
          ref={isUserShell ? detailScrollRef : undefined}
          style={{
            padding: "0 10px 22px",
            borderTop: "1px solid var(--border-color)",
            maxHeight: "min(60vh, 720px)",
            overflowY: "auto",
            WebkitOverflowScrolling: "touch",
            overscrollBehavior: isUserShell ? "auto" : undefined,
          }}
        >
          {isUserShell ? (
            <AnsiOutput text={userShellText} onRendered={scrollUserShellDetailsToBottom} />
          ) : isExecute ? (
            <ExecuteToolDetails
              command={executeCommand}
              output={executeOutputText}
            />
          ) : loadingDetails ? (
            <div style={{ marginTop: "10px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("common.loading")}</div>
          ) : detailLoadFailed && !hasContent && !hasLocations && !hasResult && !hasCollabDetails ? (
            <div style={{ marginTop: "10px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("common.loadingFailed")}</div>
          ) : isCollabTool ? (
            <CollabToolDetails meta={effectiveMeta} rootId={rootId} />
          ) : hasStructuredDetails ? (
            <div style={{ display: "flex", flexDirection: "column", gap: "14px", marginTop: "10px" }}>
              {detailSections.map((section, index) => (
                <div key={`${section.type}-${index}`} style={{ minWidth: 0 }}>
                  {section.type === "diff" ? (
                    <>
                      <div
                        style={{
                          marginBottom: "6px",
                          fontSize: "12px",
                          fontWeight: 600,
                          color: "var(--text-primary)",
                          wordBreak: "break-all",
                        }}
                      >
                        {section.path}
                      </div>
                      <MarkdownViewer content={section.markdown} root={rootId || undefined} />
                    </>
                  ) : (
                    <MarkdownViewer content={section.markdown} root={rootId || undefined} />
                  )}
                </div>
              ))}
            </div>
          ) : hasLocations ? (
            <div
              style={{
                marginTop: "10px",
                fontSize: "11px",
                color: "var(--text-secondary)",
                display: "flex",
                flexDirection: "column",
                gap: "2px",
                minWidth: 0,
              }}
            >
              {effectiveLocations!.slice(0, 3).map((loc, idx) => (
                <div
                  key={`${loc.path}-${loc.line ?? 0}-${idx}`}
                  style={{ wordBreak: "break-all", whiteSpace: "normal" }}
                >
                  {normalizeDisplayPath(loc.path, rootPath)}
                  {typeof loc.line === "number" ? `:${loc.line}` : ""}
                </div>
              ))}
              {effectiveLocations!.length > 3 && <div>{t("common.moreLocations", { count: effectiveLocations!.length - 3 })}</div>}
            </div>
          ) : null}
          {!hasStructuredDetails && hasResult && <MarkdownViewer content={result || ""} root={rootId || undefined} />}
        </div>
      )}

      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.5; }
        }
      `}</style>
    </div>
      {showProgress ? (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "5px",
            marginTop: "3px",
            paddingLeft: "8px",
            paddingRight: "4px",
            color: "var(--text-secondary)",
            fontSize: "11px",
            lineHeight: 1.25,
            minWidth: 0,
          }}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="13"
            height="13"
            viewBox="0 0 36 36"
            aria-hidden="true"
            style={{
              flexShrink: 0,
              opacity: isRunning ? 1 : 0.72,
            }}
          >
            <path d="M0 0h36v36H0z" fill="none" />
            <path fill="#ffe8b6" d="M21 18c0-2.001 3.246-3.369 5-6c2-3 2-10 2-10H8s0 7 2 10c1.754 2.631 5 3.999 5 6s-3.246 3.369-5 6c-2 3-2 10-2 10h20s0-7-2-10c-1.754-2.631-5-3.999-5-6" />
            <path fill="#ffac33" d="M18 2h-8s0 4 1 7c1.304 3.912 6 4.999 6 9s0 13 1 13s1-9 1-13s4.697-5.088 6-9c1-3 1-7 1-7z" />
            <path fill="#3b88c3" d="M30 34a2 2 0 0 1-2 2H8a2 2 0 0 1 0-4h20a2 2 0 0 1 2 2m0-32a2 2 0 0 1-2 2H8a2 2 0 0 1 0-4h20a2 2 0 0 1 2 2" />
          </svg>
          <span
            style={{
              minWidth: 0,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {progressText}
          </span>
        </div>
      ) : null}
    </>
  );
});
