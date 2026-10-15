import React, { memo } from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon } from "../agent/ModeIcon";
import { renderToolIcon } from "../stream/toolCallIcons";
import { taskAuxBadgeStyle } from "../task/taskIcons";
import { useI18n } from "../../i18n/index";
import {
  shellBadgeLabel,
} from "./sessionListShared";
import {
  formatTime,
  renderHighlightedText,
  iconButtonStyle,
  ChevronLeftIcon,
  SubSessionIcon,
  inlineActionStyle,
} from "./sessionCardHelpers";
import type { SessionItem } from "./sessionListTypes";
import { sessionActivityAt } from "../../shared/sessionActivityAt";
import { useSessionCard } from "./useSessionCard";
import { SessionCardOverflowMenu } from "./SessionCardOverflowMenu";
import { SessionStatusBadges } from "./SessionStatusBadges";

function SessionCard({
  session,
  sessionByKey,
  selected,
  nodeColor,
  parentHighlighted,
  highlightQuery,
  syncing = false,
  onSelect,
  onSync,
  onPin,
  onRename,
  onDelete,
  onArchive,
}: {
  session: SessionItem;
  sessionByKey: Map<string, SessionItem>;
  selected: boolean;
  nodeColor?: string;
  parentHighlighted?: boolean;
  highlightQuery?: string;
  syncing?: boolean;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
}) {
  const card = useSessionCard({ session, sessionByKey, selected, nodeColor, parentHighlighted, syncing, onRename });
  const {
    t,
    locale,
    isClosed,
    isPinned,
    isArchived,
    isSubagent,
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
    hexToRgbaApp,
  } = card;

  return (
    <div
      style={{
        width: "100%",
        display: "flex",
        alignItems: "center",
        gap: 0,
        padding: "2px 0",
        borderRadius: "8px",
        position: "relative",
        paddingLeft: isSubagent ? "10px" : 0,
      }}
    >
      <div
        style={{
          textAlign: "left" as const,
          padding: editing ? "7px 0 7px 2px" : "7px 4px 7px 2px",
          borderRadius: "8px",
          border: "1px solid transparent",
          background: rowBackground,
          flex: 1,
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          gap: isSubagent ? "3px" : "6px",
          transition: "all 0.15s ease",
        }}
      >
        {!isSearchResult ? (
          <span
            style={{
              position: "relative",
              width: "18px",
              height: "18px",
              flexShrink: 0,
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            {isSubagent ? <SubSessionIcon color={effectiveNodeColor ? `color-mix(in srgb, ${effectiveNodeColor} 78%, var(--text-secondary))` : undefined} /> : <ModeIcon type={session.task_id ? "task" : session.type || "chat"} size={16} color={effectiveNodeColor || undefined} />}
            {!isSubagent && session.aux_flags?.ask_user_waiting ? (
              <span
                aria-label={t("sessionList.awaitingUser")}
                title={t("sessionList.awaitingUser")}
                style={taskAuxBadgeStyle(true)}
              >
                {renderToolIcon("ask_user")}
              </span>
            ) : null}
            {!isSubagent && session.type === "command" ? (
              <span
                title={session.shell || "shell"}
                style={{
                  position: "absolute",
                  right: "-8px",
                  bottom: "-4px",
                  minWidth: 0,
                  maxWidth: "26px",
                  height: "auto",
                  padding: "1px 3px",
                  borderRadius: "5px",
                  background: "#1d4ed8",
                  border: "none",
                  color: "#fff",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  fontSize: "7px",
                  fontWeight: 700,
                  lineHeight: 1.1,
                  letterSpacing: 0,
                }}
              >
                {shellBadgeLabel(session.shell)}
              </span>
            ) : !isSubagent ? (
              <span
                style={{
                  position: "absolute",
                  right: "-2px",
                  bottom: "-2px",
                  width: "10px",
                  height: "10px",
                  borderRadius: "999px",
                  background: "var(--content-bg, #fff)",
                  border: "1px solid rgba(255,255,255,0.9)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  overflow: "hidden",
                }}
              >
                <AgentIcon
                  agentName={session.agent || ""}
                  style={{ width: "10px", height: "10px", display: "block" }}
                />
              </span>
            ) : null}
          </span>
        ) : null}

        {editing ? (
          <input
            ref={inputRef}
            value={draftName}
            disabled={saving}
            onChange={(e) => {
              setDraftName(e.target.value);
              e.currentTarget.scrollLeft = e.currentTarget.scrollWidth;
            }}
            onClick={(e) => e.stopPropagation()}
            onCompositionStart={() => {
              composingRef.current = true;
            }}
            onCompositionEnd={() => {
              composingRef.current = false;
            }}
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === "Escape") {
                e.preventDefault();
                cancelEditing();
                return;
              }
              if (e.key !== "Enter") {
                return;
              }
              const nativeEvent = e.nativeEvent as KeyboardEvent;
              const isComposing =
                composingRef.current ||
                nativeEvent.isComposing ||
                nativeEvent.keyCode === 229;
              if (isComposing) {
                return;
              }
              e.preventDefault();
              void submitRename();
            }}
            style={{
              minWidth: 0,
              flex: 1,
              height: "28px",
              borderRadius: "6px",
              border: "1px solid var(--accent-color)",
              background: "var(--content-bg, #fff)",
              color: "var(--text-primary)",
              fontSize: "13px",
              fontWeight: 600,
              padding: "0 10px 0 8px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        ) : (
          <button
            type="button"
            onClick={() => onSelect?.(session)}
            style={{
              minWidth: 0,
              flex: 1,
              border: "none",
              background: "transparent",
              padding: 0,
              cursor: "pointer",
              textAlign: "left",
              color: selected ? (effectiveNodeColor || "var(--accent-color)") : "var(--text-primary)",
            }}
            onMouseEnter={(e) => {
              const container = e.currentTarget.parentElement;
              if (container && !selected && !parentHighlighted) {
                container.style.background = "rgba(0,0,0,0.03)";
              }
            }}
            onMouseLeave={(e) => {
              const container = e.currentTarget.parentElement;
              if (container && !selected && !parentHighlighted) {
                container.style.background = "transparent";
              }
            }}
          >
            <span
              style={{
                display: "block",
                fontSize: "13px",
                fontWeight: selected ? 600 : 500,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {renderHighlightedText(displayName, highlightQuery, {
                color: selected ? (effectiveNodeColor || "var(--accent-color)") : "var(--text-primary)",
              })}
            </span>
            {!isSubagent ? (
              <SessionStatusBadges
                auxFlags={session.aux_flags}
                relatedWorktree={session.related_worktree}
                worktreeMissing={session.worktree_missing}
                nodeColor={effectiveNodeColor || undefined}
              />
            ) : null}
            {snippet ? (
              <span
                style={{
                  marginTop: "2px",
                  display: "block",
                  fontSize: "11px",
                  lineHeight: 1.45,
                  color: "var(--text-secondary)",
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                }}
              >
                {renderHighlightedText(snippet, highlightQuery, {
                  color: "var(--text-secondary)",
                })}
              </span>
            ) : null}
          </button>
        )}

        {editing ? (
          <div
            style={{
              flexShrink: 0,
              display: "inline-flex",
              alignItems: "center",
              gap: 0,
            }}
          >
            <button
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.stopPropagation();
                void submitRename();
              }}
              disabled={saving}
              aria-label={t("sessionList.confirmRename")}
              style={{
                ...inlineActionStyle,
                width: "18px",
                color: "var(--accent-color)",
                opacity: saving ? 0.6 : 1,
                cursor: saving ? "default" : "pointer",
              }}
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <polyline points="20 6 9 17 4 12" />
              </svg>
            </button>
            <button
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.stopPropagation();
                cancelEditing();
              }}
              disabled={saving}
              aria-label={t("sessionList.cancelRename")}
              style={{
                ...inlineActionStyle,
                width: "18px",
                opacity: saving ? 0.6 : 1,
                cursor: saving ? "default" : "pointer",
              }}
            >
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        ) : null}

      </div>

      {!editing ? (
        <div
          style={{
            flexShrink: 0,
            minWidth: "24px",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-end",
            paddingLeft: "2px",
          }}
        >
          {syncing ? (
            <span
              aria-label={t("sessionList.syncing")}
              title={t("sessionList.syncing")}
              style={{
                width: "12px",
                height: "12px",
                borderRadius: "50%",
                border: "1.5px solid rgba(100,116,139,0.35)",
                borderTopColor: "var(--accent-color)",
                display: "inline-block",
                flexShrink: 0,
                animation: "spin 0.8s linear infinite",
              }}
            />
          ) : session.presence === "awaiting" || session.presence === "delegating" ? (
            // 空心：不在跑，但也不是"全闲"。两档文案分开 —— 「等你回答」是让用户做事，
            // 「子代理运行中」是让用户等，混成一句两边都会误读。
            <span
              aria-label={t(session.presence === "awaiting" ? "sessionList.awaitingAnswer" : "sessionList.delegating")}
              title={t(session.presence === "awaiting" ? "sessionList.awaitingAnswer" : "sessionList.delegating")}
              style={{
                width: "8px",
                height: "8px",
                borderRadius: "999px",
                flexShrink: 0,
                boxSizing: "border-box",
                border: `1.5px solid ${effectiveNodeColor || "var(--accent-color)"}`,
                background: "transparent",
              }}
            />
          ) : session.pending ? (
            <span
              aria-label={t("sessionList.replying")}
              title={t("sessionList.replying")}
              style={{
                width: "8px",
                height: "8px",
                borderRadius: "999px",
                flexShrink: 0,
                boxSizing: "border-box",
                border: `1.5px solid ${effectiveNodeColor || "var(--accent-color)"}`,
                background: effectiveNodeColor || "var(--accent-color)",
                animation: "mindfs-bound-pulse 2.2s ease-in-out infinite",
                boxShadow: `0 0 0 1.5px ${hexToRgbaApp(effectiveNodeColor || "var(--accent-color)", 0.14)}`,
              }}
            />
          ) : (
            <span
              style={{
                fontSize: "10px",
                color: "var(--text-secondary)",
                opacity: 0.8,
                whiteSpace: "nowrap",
                textAlign: "right",
              }}
            >
              {formatTime(
                isClosed && session.closed_at
                  ? session.closed_at
                  : sessionActivityAt(session),
                locale,
                t("time.justNow"),
              )}
            </span>
          )}
        </div>
      ) : null}

      <SessionCardOverflowMenu
        session={session}
        menuRef={menuRef}
        menuOpen={menuOpen}
        isPinned={isPinned}
        isArchived={isArchived}
        selected={selected}
        syncing={syncing}
        storedName={storedName}
        onToggle={() => setMenuOpen((open) => !open)}
        onClose={() => setMenuOpen(false)}
        onPin={onPin}
        onSync={onSync}
        onRename={(name) => {
          setDraftName(name);
          setEditing(true);
        }}
        onArchive={onArchive}
        onDelete={onDelete}
      />
    </div>
  );
}

// memo 化 SessionCard：父组件（SessionList / MultiProjectSessionList）重渲染时，
// 若 props 引用未变则跳过整卡重渲染（含 useI18n / 多个 useEffect）。
export const SessionCardMemo = memo(SessionCard);
