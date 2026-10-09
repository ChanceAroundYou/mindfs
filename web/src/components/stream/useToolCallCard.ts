import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { sessionService, type ToolCall, type ToolCallContentItem, type ToolCallLocation } from "../../services/session";
import { buildDetailSections, normalizeDisplayPath, isDiffLikeText, extractDiffPath } from "./toolCallSections";
import { basename, shouldPreserveDisplayStatus, isRunningStatus, stringMeta, toolProgressText, extractExecuteCommand } from "./toolCallHelpers";
import { renderToolIcon } from "./toolCallIcons";
import { statusColors } from "./toolCallRender";

export type ToolCallCardProps = {
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

export function useToolCallCard({
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

  return {
    expanded,
    setExpanded,
    result,
    loadingDetails,
    detailLoadFailed,
    detailScrollRef,
    effectiveMeta,
    effectiveLocations,
    executeCommand,
    userShellText,
    executeOutputText,
    hasContent,
    hasLocations,
    hasResult,
    hasCollabDetails,
    hasDetails,
    icon,
    detailSections,
    isFileChange,
    displayFileNames,
    label,
    isRunning,
    isFailed,
    progressText,
    showProgress,
    hasStructuredDetails,
    statusColor,
    scrollUserShellDetailsToBottom,
    isUserShell,
    isExecute,
    isCollabTool,
  };
}
