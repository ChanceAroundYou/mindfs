import React from "react";
import { useI18n } from "../../i18n/index";
import { LOCAL_NODE_ID, PALETTE } from "../../shared/nodePalette";
import { useNodeManagerPanel } from "./useNodeManagerPanel";

// 挂载即"打开"（与它替换掉的 add/remove/edit 三个 popover 一致），外部点击由面板自己收。
type NodeManagerPanelProps = {
  onClose: () => void;
  onRefreshAll?: () => Promise<void>;
};

const PANEL_STYLE: React.CSSProperties = {
  position: "absolute",
  top: "calc(100% + 6px)",
  left: "8px",
  right: "8px",
  zIndex: 35,
  padding: "12px",
  borderRadius: 12,
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15,23,42,0.14)",
  display: "flex",
  flexDirection: "column",
  gap: 8,
  maxHeight: 380,
  overflow: "auto",
};

const INPUT_STYLE: React.CSSProperties = {
  width: "100%",
  borderRadius: 8,
  border: "1px solid var(--border-color)",
  padding: "8px 10px",
  fontSize: 12,
  boxSizing: "border-box",
};

const SMALL_BUTTON_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const PRIMARY_BUTTON_STYLE: React.CSSProperties = {
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
};

export function NodeManagerPanel({ onClose, onRefreshAll }: NodeManagerPanelProps): React.ReactElement {
  const { t } = useI18n();
  const {
    rootRef,
    nodes,
    editingId,
    draftName,
    draftUrl,
    draftColor,
    editBusy,
    addOpen,
    addName,
    addUrl,
    addBusy,
    addError,
    error,
    statusLabel,
    refreshSpin,
    setEditingId,
    setDraftName,
    setDraftUrl,
    setDraftColor,
    setAddOpen,
    setAddName,
    setAddUrl,
    setAddError,
    startEdit,
    commitEdit,
    handleDelete,
    handleAdd,
  } = useNodeManagerPanel({ onClose, onRefreshAll });

  return (
    <div ref={rootRef} style={PANEL_STYLE}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ fontWeight: 700, fontSize: 12, flex: 1 }}>{t("nodeManager.title")}</div>
        <button
          type="button"
          aria-label={t("common.refresh")}
          title={t("common.refresh")}
          disabled={refreshSpin.refreshing}
          onClick={() => void refreshSpin.handleClick()}
          onMouseDown={() => refreshSpin.setPressed(true)}
          onMouseUp={() => refreshSpin.setPressed(false)}
          onMouseLeave={() => refreshSpin.setPressed(false)}
          style={{
            ...SMALL_BUTTON_STYLE,
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            opacity: refreshSpin.refreshing ? 0.6 : 1,
            cursor: refreshSpin.refreshing ? "not-allowed" : "pointer",
          }}
        >
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={refreshSpin.refreshing ? { animation: "mindfs-update-spin 0.8s linear infinite" } : undefined}
          >
            <path d="M21 12a9 9 0 1 1-2.64-6.36" />
            <path d="M21 3v6h-6" />
          </svg>
          <span>{t("common.refresh")}</span>
        </button>
      </div>

      {nodes.map((node) => {
        const isLocal = node.id === LOCAL_NODE_ID;
        const status = statusLabel(node.id);
        const editing = editingId === node.id;
        return (
          <div
            key={node.id}
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 8,
              border: "1px solid var(--border-color)",
              borderRadius: 8,
              padding: "8px 10px",
            }}
          >
            {/* 编辑态：名称与路径的纯文本在这一行原地换成输入框，不再另起一组重复输入框 */}
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span
                  style={{ width: 8, height: 8, borderRadius: "50%", background: node.color, display: "inline-block", flex: "0 0 auto" }}
                />
                {editing ? (
                  <input
                    value={draftName}
                    onChange={(e) => setDraftName(e.target.value)}
                    placeholder={t("login.nodeNamePlaceholder")}
                    autoFocus
                    style={{ ...INPUT_STYLE, flex: 1, minWidth: 0, padding: "4px 8px" }}
                  />
                ) : (
                  <span style={{ flex: 1, fontSize: 12, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {node.name}
                  </span>
                )}
                <span style={{ fontSize: 11, color: status.color, whiteSpace: "nowrap" }}>{status.text}</span>
              </div>

              {editing ? (
                <input
                  value={draftUrl}
                  onChange={(e) => setDraftUrl(e.target.value)}
                  placeholder={isLocal ? t("nodeManager.thisDevice") : t("login.nodeUrlPlaceholder")}
                  disabled={isLocal}
                  style={{
                    ...INPUT_STYLE,
                    fontSize: 11,
                    padding: "4px 8px",
                    opacity: isLocal ? 0.6 : 1,
                    cursor: isLocal ? "not-allowed" : "text",
                  }}
                />
              ) : (
                <div style={{ fontSize: 11, color: "var(--text-secondary)", overflowWrap: "anywhere" }}>
                  {node.url || t("nodeManager.thisDevice")}
                </div>
              )}
            </div>

            {editing ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ fontSize: 11, color: "var(--text-secondary)" }}>{t("nodeManager.color")}</span>
                  {PALETTE.map((color) => (
                    <button
                      key={color}
                      type="button"
                      aria-label={color}
                      onClick={() => setDraftColor(color)}
                      style={{
                        width: 18,
                        height: 18,
                        borderRadius: "50%",
                        background: color,
                        border: draftColor.toLowerCase() === color.toLowerCase() ? "2px solid var(--text-primary)" : "2px solid transparent",
                        cursor: "pointer",
                        padding: 0,
                      }}
                    />
                  ))}
                </div>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
                  <button type="button" onClick={() => setEditingId(null)} style={SMALL_BUTTON_STYLE}>
                    {t("common.cancel")}
                  </button>
                  <button
                    type="button"
                    disabled={!draftName.trim() || editBusy}
                    onClick={() => void commitEdit(node)}
                    style={{
                      ...PRIMARY_BUTTON_STYLE,
                      opacity: draftName.trim() && !editBusy ? 1 : 0.6,
                      cursor: editBusy ? "not-allowed" : "pointer",
                    }}
                  >
                    {editBusy ? t("nodeManager.checking") : t("common.save")}
                  </button>
                </div>
              </div>
            ) : (
              <div style={{ display: "flex", gap: 8 }}>
                <button type="button" onClick={() => startEdit(node)} style={SMALL_BUTTON_STYLE}>
                  {t("common.edit")}
                </button>
                {!isLocal ? (
                  <button
                    type="button"
                    onClick={() => void handleDelete(node)}
                    style={{ ...SMALL_BUTTON_STYLE, borderColor: "#dc2626", color: "#dc2626" }}
                  >
                    {t("common.delete")}
                  </button>
                ) : null}
              </div>
            )}
          </div>
        );
      })}

      {addOpen ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <input
            value={addName}
            onChange={(e) => setAddName(e.target.value)}
            placeholder={t("login.nodeNamePlaceholder")}
            style={INPUT_STYLE}
          />
          <input
            value={addUrl}
            onChange={(e) => setAddUrl(e.target.value)}
            placeholder={t("login.nodeUrlPlaceholder")}
            style={INPUT_STYLE}
          />
          {addError ? <div style={{ color: "#dc2626", fontSize: 12 }}>{addError}</div> : null}
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <button type="button" onClick={() => { setAddOpen(false); setAddError(""); }} style={SMALL_BUTTON_STYLE}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              disabled={addBusy || !addName.trim() || !addUrl.trim()}
              onClick={() => void handleAdd()}
              style={{
                ...PRIMARY_BUTTON_STYLE,
                opacity: addBusy || !addName.trim() || !addUrl.trim() ? 0.6 : 1,
                cursor: addBusy ? "not-allowed" : "pointer",
              }}
            >
              {addBusy ? t("nodeManager.checking") : t("common.save")}
            </button>
          </div>
        </div>
      ) : (
        <button type="button" onClick={() => { setAddOpen(true); setAddError(""); }} style={SMALL_BUTTON_STYLE}>
          {t("nodeManager.addNode")}
        </button>
      )}

      {error ? <div style={{ color: "#dc2626", fontSize: 12 }}>{error}</div> : null}
    </div>
  );
}
