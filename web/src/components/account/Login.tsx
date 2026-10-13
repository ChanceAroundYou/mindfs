import React, { type ReactElement } from "react";
import { useI18n } from "../../i18n/index";
import { useLogin } from "./useLogin";
import { LoginUpdateNotes } from "./LoginUpdateNotes";

const LAUNCHER_BG =
  "radial-gradient(circle at top left, rgba(91, 125, 184, 0.07), transparent 22%), radial-gradient(circle at right 18%, rgba(148, 163, 184, 0.18), transparent 24%), linear-gradient(180deg, #f8fafc 0%, #edf2f7 100%)";
const SURFACE = "var(--mindfs-launcher-surface)";
const SURFACE_STRONG = "var(--mindfs-launcher-surface-strong)";
const BORDER = "var(--mindfs-launcher-border)";
const BORDER_STRONG = "var(--mindfs-launcher-border-strong)";
const TEXT = "var(--mindfs-launcher-text)";
const MUTED = "var(--mindfs-launcher-muted)";
const ACCENT = "var(--mindfs-launcher-accent)";
const SHADOW = "var(--mindfs-launcher-shadow)";

type LoginProps = {
  onOpenNode: (nodeURL: string) => void;
};

export function Login({ onOpenNode }: LoginProps): ReactElement {
  const { t } = useI18n();
  const {
    nodes,
    composerOpen,
    setComposerOpen,
    nodeName,
    setNodeName,
    nodeURL,
    setNodeURL,
    formError,
    setFormError,
    editingNodeID,
    setEditingNodeID,
    editingNodeName,
    setEditingNodeName,
    appUpdateNotesOpen,
    setAppUpdateNotesOpen,
    persistNodes,
    openNode,
    handleDeleteNode,
    handleStartRename,
    handleCancelRename,
    handleCommitRename,
    handleSaveNode,
    handleDownloadAppUpdate,
    showAppUpdate,
    appUpdateDisabled,
    appUpdateText,
    appUpdateHelp,
    appUpdateNotes,
  } = useLogin({ onOpenNode });

  return (
    <div
      style={{
        minHeight: "100dvh",
        background: "var(--mindfs-system-bar-bg)",
        color: TEXT,
        padding:
          "calc(var(--mindfs-safe-area-top, env(safe-area-inset-top, 0px)) + 20px) 16px calc(var(--mindfs-safe-area-bottom, env(safe-area-inset-bottom, 0px)) + 24px)",
        display: "flex",
        justifyContent: "center",
      }}
    >
      <div
        style={{
          position: "fixed",
          inset: 0,
          background: `var(--mindfs-launcher-bg, ${LAUNCHER_BG})`,
          pointerEvents: "none",
          zIndex: 0,
        }}
      />
      <div
        style={{
          position: "relative",
          zIndex: 1,
          width: "100%",
          maxWidth: "640px",
          minHeight:
            "calc(100dvh - var(--mindfs-safe-area-top, env(safe-area-inset-top, 0px)) - var(--mindfs-safe-area-bottom, env(safe-area-inset-bottom, 0px)) - 44px)",
          display: "flex",
          flexDirection: "column",
          gap: "8px",
        }}
      >
        {nodes.map((node) => (
          <div
            key={node.id}
            style={{
              width: "100%",
              borderRadius: "20px",
              border: `1px solid ${BORDER}`,
              background: SURFACE,
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              gap: "16px",
              boxShadow: SHADOW,
              backdropFilter: "blur(20px)",
            }}
          >
            <div
              onClick={() => {
                if (editingNodeID !== node.id) {
                  openNode(node);
                }
              }}
              style={{
                flex: 1,
                minWidth: 0,
                textAlign: "left",
                padding: "16px 0 16px 18px",
                cursor: editingNodeID === node.id ? "default" : "pointer",
              }}
            >
              <div style={{ minWidth: 0, display: "grid", gap: "4px" }}>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    minWidth: 0,
                  }}
                >
                  {editingNodeID === node.id ? (
                    <input
                      type="text"
                      value={editingNodeName}
                      autoFocus
                      onClick={(event) => event.stopPropagation()}
                      onChange={(event) => setEditingNodeName(event.target.value)}
                      onBlur={() => handleCommitRename(node)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          handleCommitRename(node);
                        }
                        if (event.key === "Escape") {
                          event.preventDefault();
                          handleCancelRename();
                        }
                      }}
                      style={{
                        flex: 1,
                        minWidth: 0,
                        borderRadius: "10px",
                        border: `1px solid ${BORDER_STRONG}`,
                        background: "var(--mindfs-launcher-input-bg)",
                        padding: "6px 10px",
                        fontSize: "17px",
                        fontWeight: 500,
                        color: TEXT,
                        lineHeight: 1.2,
                        outline: "none",
                      }}
                    />
                  ) : (
                    <>
                      <div
                        style={{
                          fontSize: "17px",
                          fontWeight: 500,
                          color: TEXT,
                          lineHeight: 1.2,
                          wordBreak: "break-word",
                        }}
                      >
                        {node.name}
                      </div>
                      <button
                        type="button"
                        aria-label={t("login.renameNode", { name: node.name })}
                        onClick={(event) => {
                          event.stopPropagation();
                          handleStartRename(node);
                        }}
                        style={{
                          border: "none",
                          background: "transparent",
                          color: MUTED,
                          display: "inline-flex",
                          alignItems: "center",
                          justifyContent: "center",
                          width: "22px",
                          height: "22px",
                          padding: 0,
                          cursor: "pointer",
                          flex: "0 0 auto",
                        }}
                      >
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                          <path d="M12 20h9" />
                          <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4Z" />
                        </svg>
                      </button>
                    </>
                  )}
                </div>
                <div
                  style={{
                    fontSize: "12px",
                    lineHeight: 1.5,
                    color: MUTED,
                    wordBreak: "break-word",
                  }}
                >
                  {node.url}
                </div>
              </div>
            </div>
            <button
              type="button"
              aria-label={t("login.deleteNode", { name: node.name })}
              onClick={(event) => {
                event.stopPropagation();
                handleDeleteNode(node.id);
              }}
              style={{
                border: "none",
                background: "transparent",
                color: ACCENT,
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                width: "48px",
                height: "100%",
                minHeight: "72px",
                padding: "0 14px 0 0",
                cursor: "pointer",
                flex: "0 0 auto",
              }}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M3 6h18" />
                <path d="M8 6V4h8v2" />
                <path d="M19 6l-1 14H6L5 6" />
                <path d="M10 11v6" />
                <path d="M14 11v6" />
              </svg>
            </button>
          </div>
        ))}

        {showAppUpdate ? (
          <div
            style={{
              border: `1px solid ${BORDER}`,
              borderRadius: "20px",
              background: SURFACE_STRONG,
              padding: "14px",
              boxShadow: SHADOW,
              backdropFilter: "blur(20px)",
              display: "grid",
              gap: "10px",
              marginTop: "auto",
            }}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "12px",
              }}
            >
              <div style={{ minWidth: 0, display: "grid", gap: "3px" }}>
                <div
                  style={{
                    fontSize: "15px",
                    fontWeight: 600,
                    color: TEXT,
                    lineHeight: 1.25,
                  }}
                >
                  {t("update.newVersion")}
                </div>
                {appUpdateHelp ? (
                  <div
                    style={{
                      fontSize: "12px",
                      color: MUTED,
                      lineHeight: 1.45,
                      wordBreak: "break-word",
                    }}
                  >
                    {appUpdateHelp}
                  </div>
                ) : null}
              </div>
              <button
                type="button"
                disabled={appUpdateDisabled}
                onClick={() => {
                  void handleDownloadAppUpdate();
                }}
                style={{
                  border: "none",
                  borderRadius: "14px",
                  background: appUpdateDisabled ? "rgba(148, 163, 184, 0.35)" : ACCENT,
                  color: appUpdateDisabled ? MUTED : "#fff8f2",
                  padding: "10px 14px",
                  fontSize: "13px",
                  fontWeight: 600,
                  cursor: appUpdateDisabled ? "not-allowed" : "pointer",
                  flexShrink: 0,
                  minWidth: "86px",
                }}
              >
                {appUpdateText}
              </button>
            </div>
            <LoginUpdateNotes
              notes={appUpdateNotes}
              open={appUpdateNotesOpen}
              onToggle={() => setAppUpdateNotesOpen((open) => !open)}
            />
          </div>
        ) : null}

        <button
          type="button"
          onClick={() => {
            setComposerOpen(true);
            setFormError("");
          }}
          style={{
            width: "100%",
            textAlign: "center",
            border: `1px dashed ${BORDER_STRONG}`,
            borderRadius: "20px",
            background: "var(--mindfs-launcher-surface-soft)",
            padding: "18px",
            fontSize: "24px",
            fontWeight: 500,
            color: MUTED,
            cursor: "pointer",
            boxShadow: SHADOW,
            backdropFilter: "blur(20px)",
            marginTop: showAppUpdate ? 0 : "auto",
          }}
        >
          +
        </button>
      </div>

      {composerOpen ? (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(17, 24, 39, 0.18)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: "20px",
            zIndex: 40,
          }}
        >
          <form
            onSubmit={handleSaveNode}
            style={{
              width: "100%",
              maxWidth: "420px",
              borderRadius: "22px",
              background: SURFACE_STRONG,
              border: `1px solid var(--mindfs-launcher-modal-border)`,
              boxShadow: SHADOW,
              padding: "18px",
              backdropFilter: "blur(20px)",
              display: "grid",
              gap: "12px",
            }}
          >
            <input
              type="text"
              value={nodeName}
              onChange={(event) => setNodeName(event.target.value)}
              placeholder={t("login.nodeNamePlaceholder")}
              autoFocus
              style={{
                width: "100%",
                borderRadius: "14px",
                border: `1px solid ${formError ? "var(--mindfs-launcher-error-text)" : BORDER_STRONG}`,
                background: "var(--mindfs-launcher-input-bg)",
                padding: "14px 15px",
                fontSize: "15px",
                color: TEXT,
                outline: "none",
                boxSizing: "border-box",
              }}
            />

            <input
              type="text"
              value={nodeURL}
              onChange={(event) => setNodeURL(event.target.value)}
              placeholder={t("login.nodeUrlPlaceholder")}
              spellCheck={false}
              style={{
                width: "100%",
                borderRadius: "14px",
                border: `1px solid ${formError ? "var(--mindfs-launcher-error-text)" : BORDER_STRONG}`,
                background: "var(--mindfs-launcher-input-bg)",
                padding: "14px 15px",
                fontSize: "15px",
                color: TEXT,
                outline: "none",
                boxSizing: "border-box",
              }}
            />

            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "10px",
              }}
            >
              <button
                type="button"
                onClick={() => {
                  setComposerOpen(false);
                  setFormError("");
                }}
                style={{
                  border: `1px solid ${BORDER_STRONG}`,
                  borderRadius: "14px",
                  padding: "12px 16px",
                  background: "transparent",
                  color: MUTED,
                  fontSize: "14px",
                  fontWeight: 500,
                  cursor: "pointer",
                  width: "100%",
                }}
              >
                {t("common.cancel")}
              </button>
              <button
                type="submit"
                style={{
                  border: "none",
                  borderRadius: "14px",
                  padding: "12px 16px",
                  background: ACCENT,
                  color: "#fff8f2",
                  fontSize: "14px",
                  fontWeight: 500,
                  cursor: "pointer",
                  width: "100%",
                }}
              >
                {t("common.save")}
              </button>
            </div>
          </form>
        </div>
      ) : null}
    </div>
  );
}
