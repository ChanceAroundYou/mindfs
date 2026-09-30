import React, { useCallback, useEffect, useState } from "react";
import { renderToolIcon } from "../stream/ToolCallCard";
import { useI18n } from "../../i18n";
import { type QueuedMessageInfo } from "./types";

/** 编辑态是本组件私有的：向父层只暴露保存/取消/删除/立即发送四个动作。 */
export function QueuedMessagesList({
  queuedMessages,
  accentColorRaw,
  isMobile,
  onRemove,
  onUpdate,
  onSendNow,
}: {
  queuedMessages: QueuedMessageInfo[];
  accentColorRaw: string;
  isMobile: boolean;
  onRemove: (id: string) => void | Promise<void>;
  onUpdate: (id: string, content: string) => void | Promise<void>;
  onSendNow: (id: string) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [editingQueueId, setEditingQueueId] = useState<string | null>(null);
  const [editingQueueText, setEditingQueueText] = useState("");

  const startEdit = useCallback((item: QueuedMessageInfo) => {
    setEditingQueueId(item.id);
    setEditingQueueText(item.content || "");
  }, []);

  const saveEdit = useCallback(async () => {
    const queueId = editingQueueId;
    const nextText = editingQueueText.trim();
    if (!queueId || !nextText) return;
    await onUpdate(queueId, nextText);
    setEditingQueueId(null);
    setEditingQueueText("");
  }, [editingQueueId, editingQueueText, onUpdate]);

  const cancelEdit = useCallback(() => {
    setEditingQueueId(null);
    setEditingQueueText("");
  }, []);

  useEffect(() => {
    if (!editingQueueId) return;
    if (!queuedMessages.some((item) => item.id === editingQueueId)) {
      cancelEdit();
    }
  }, [cancelEdit, editingQueueId, queuedMessages]);

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "3px",
        padding: isMobile ? "0 31px 4px" : "0 0 4px",
        maxHeight: isMobile ? "116px" : "144px",
        overflowY: "auto",
        scrollbarWidth: "thin",
      }}
    >
      {queuedMessages.map((item) => (
        <div
          key={item.id}
          style={{
            position: "relative",
            display: "grid",
            gridTemplateColumns: "minmax(0, 1fr) auto",
            alignItems: "center",
            gap: "6px",
            minHeight: "28px",
            padding: "2px 3px 2px 9px",
            border: `1px solid color-mix(in srgb, ${accentColorRaw} 32%, transparent)`,
            borderRadius: "8px",
            background: "var(--panel-bg)",
            boxShadow: isMobile ? "none" : "var(--panel-shadow)",
          }}
        >
          {editingQueueId === item.id ? (
            <input
              value={editingQueueText}
              onChange={(event) => setEditingQueueText(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void saveEdit();
                } else if (event.key === "Escape") {
                  event.preventDefault();
                  cancelEdit();
                }
              }}
              autoFocus
              style={{
                minWidth: 0,
                height: "24px",
                border: "none",
                borderRadius: 0,
                background: "transparent",
                color: "var(--text-primary)",
                fontSize: "12px",
                padding: 0,
                outline: "none",
              }}
            />
          ) : (
            <div
              title={item.content}
              style={{
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                fontSize: "12px",
                color: "var(--text-secondary)",
                lineHeight: 1.35,
              }}
            >
              {item.content}
            </div>
          )}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "3px",
            }}
          >
            {editingQueueId === item.id ? (
              <>
                <button
                  type="button"
                  aria-label={t("action.queueSave")}
                  title={t("common.save")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void saveEdit()}
                  disabled={!editingQueueText.trim()}
                  style={{ width: "28px", height: "28px", border: "none", borderRadius: "7px", background: "transparent", color: editingQueueText.trim() ? accentColorRaw : "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: editingQueueText.trim() ? "pointer" : "not-allowed", opacity: editingQueueText.trim() ? 1 : 0.45 }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M20 6 9 17l-5-5" />
                  </svg>
                </button>
                <button
                  type="button"
                  aria-label={t("action.queueCancelEdit")}
                  title={t("common.cancel")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={cancelEdit}
                  style={{ width: "28px", height: "28px", border: "none", borderRadius: "7px", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M18 6 6 18" />
                    <path d="m6 6 12 12" />
                  </svg>
                </button>
              </>
            ) : (
              <>
                <button
                  type="button"
                  aria-label={t("action.queueDelete")}
                  title={t("common.delete")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void onRemove(item.id)}
                  style={{ width: "28px", height: "28px", border: "none", borderRadius: "7px", background: "transparent", color: "#dc2626", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <polyline points="3 6 5 6 21 6" />
                    <path d="M19 6l-1 14H6L5 6" />
                    <path d="M10 11v6" />
                    <path d="M14 11v6" />
                    <path d="M9 6V4h6v2" />
                  </svg>
                </button>
                <button
                  type="button"
                  aria-label={t("action.queueEdit")}
                  title={t("common.edit")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => startEdit(item)}
                  style={{ width: "28px", height: "28px", border: "none", borderRadius: "7px", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
                >
                  <span style={{ display: "inline-flex", transform: "scale(1.125)" }}>
                    {renderToolIcon("edit")}
                  </span>
                </button>
                <button
                  type="button"
                  aria-label={t("action.queueSendNow")}
                  title={t("action.queueSendNow")}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => void onSendNow(item.id)}
                  style={{ width: "28px", height: "28px", border: "none", borderRadius: "7px", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
                    <path d="M0 0h24v24H0z" fill="none" />
                    <g fill="currentColor" fillRule="evenodd" clipRule="evenodd">
                      <path d="M3 14a1 1 0 0 1 1-1h12a3 3 0 0 0 3-3V6a1 1 0 1 1 2 0v4a5 5 0 0 1-5 5H4a1 1 0 0 1-1-1" />
                      <path d="M3.293 14.707a1 1 0 0 1 0-1.414l4-4a1 1 0 0 1 1.414 1.414L5.414 14l3.293 3.293a1 1 0 1 1-1.414 1.414z" />
                    </g>
                  </svg>
                </button>
              </>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
