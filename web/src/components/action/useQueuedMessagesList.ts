import React, { useCallback, useEffect, useState } from "react";
import { type QueuedMessageInfo } from "./actionTypes";

export type QueuedMessagesListProps = {
  queuedMessages: QueuedMessageInfo[];
  accentColorRaw: string;
  isMobile: boolean;
  onRemove: (id: string) => void | Promise<void>;
  onUpdate: (id: string, content: string) => void | Promise<void>;
  onSendNow: (id: string) => void | Promise<void>;
};

export function useQueuedMessagesList({
  queuedMessages,
  onUpdate,
}: QueuedMessagesListProps) {
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

  return {
    editingQueueId,
    editingQueueText,
    setEditingQueueText,
    startEdit,
    saveEdit,
    cancelEdit,
  };
}
