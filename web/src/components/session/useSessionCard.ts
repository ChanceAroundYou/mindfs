import { useEffect, useRef, useState } from "react";
import { getNodes } from "../../services/net/nodeRegistry";
import { hexToRgbaApp } from "../../app/taskIcons";
import { resolveGroupColor } from "../../services/session/sessionGroupDisplay";
import { useI18n } from "../../i18n/index";
import { forkSessionDisplayName, parseForkSessionSource } from "./sessionListShared";
import type { SessionItem } from "./sessionListTypes";

export function useSessionCard({
  session,
  sessionByKey,
  selected,
  nodeColor,
  parentHighlighted,
  syncing = false,
  onRename,
}: {
  session: SessionItem;
  sessionByKey: Map<string, SessionItem>;
  selected: boolean;
  nodeColor?: string;
  parentHighlighted?: boolean;
  syncing?: boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
}) {
  const { locale, t } = useI18n();
  const isClosed = !!session.closed_at;
  const isPinned = !!session.pinned_at;
  const isArchived = !!session.archived_at;
  const isSubagent = !!session.parent_session_key && !parseForkSessionSource(session.source);
  const forkSource = parseForkSessionSource(session.source);
  const isForkSession = !!forkSource;
  const storedName = session.name || `Session ${session.key.slice(0, 8)}`;
  const displayName = isForkSession
    ? forkSessionDisplayName(session.name || "", forkSource, sessionByKey, session.root_id)
    : storedName;
  const snippet = (session.search_snippet || "").trim();
  const isSearchResult = !!session.search_match_type;
  const [menuOpen, setMenuOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draftName, setDraftName] = useState(storedName);
  const [saving, setSaving] = useState(false);
  const effectiveNodeColor = String(
    nodeColor ||
    (session as any)?._nodeColor ||
    resolveGroupColor(
      { rootId: String((session as any)?.root_id || ""), _nodeId: String((session as any)?._nodeId || "") },
      {},
      getNodes() as any,
    ) ||
    "",
  ).trim();
  const rowBackground = selected
    ? "var(--node-row-selected-bg)"
    : parentHighlighted
      ? "rgba(0,0,0,0.03)"
      : "transparent";
  const menuRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const composingRef = useRef(false);
  const submittingRef = useRef(false);

  useEffect(() => {
    if (!editing) {
      setDraftName(storedName);
    }
  }, [storedName, editing]);

  useEffect(() => {
    if (!menuOpen) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [menuOpen]);

  useEffect(() => {
    if (!editing) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [editing]);

  useEffect(() => {
    if (!editing) return;
    const input = inputRef.current;
    if (!input) return;
    const atEnd = input.selectionStart === input.value.length;
    if (atEnd) {
      input.scrollLeft = input.scrollWidth;
    }
  }, [draftName, editing]);

  const cancelEditing = () => {
    setEditing(false);
    setSaving(false);
    setDraftName(storedName);
  };

  const submitRename = async () => {
    if (submittingRef.current) return;
    const trimmed = draftName.trim();
    if (!trimmed) {
      cancelEditing();
      return;
    }
    if (trimmed === storedName.trim()) {
      cancelEditing();
      return;
    }
    if (!onRename) {
      cancelEditing();
      return;
    }
    submittingRef.current = true;
    setSaving(true);
    try {
      const ok = await onRename(session, trimmed);
      if (ok === false) {
        inputRef.current?.focus();
        inputRef.current?.select();
        return;
      }
      setEditing(false);
    } finally {
      submittingRef.current = false;
      setSaving(false);
    }
  };

  return {
    t,
    locale,
    isClosed,
    isPinned,
    isArchived,
    isSubagent,
    isForkSession,
    storedName,
    displayName,
    snippet,
    isSearchResult,
    menuOpen,
    setMenuOpen,
    editing,
    setEditing,
    draftName,
    setDraftName,
    saving,
    effectiveNodeColor,
    rowBackground,
    menuRef,
    inputRef,
    composingRef,
    cancelEditing,
    submitRename,
    syncing,
    hexToRgbaApp,
  };
}
