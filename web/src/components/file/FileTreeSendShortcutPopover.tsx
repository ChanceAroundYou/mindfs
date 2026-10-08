import React from "react";

export function FileTreeSendShortcutPopover(props: any) {
  const { sendShortcutOpen, sendShortcutPopoverRef, sendShortcutDraft, sendShortcutError, setSendShortcutOpen, setSendShortcutDraft, setSendShortcutError, shortcutFromKeyboardEvent, formatSendShortcut, saveSendShortcut, t, agentConfigActionRowStyle, agentConfigSecondaryButtonStyle, agentConfigPrimaryButtonStyle } = props;
  return (sendShortcutOpen ? (
          <div
            ref={sendShortcutPopoverRef}
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              left: "8px",
              right: "3px",
              zIndex: 40,
              padding: "14px",
              borderRadius: "12px",
              border: "1px solid var(--border-color)",
              background: "var(--menu-bg)",
              boxShadow: "0 16px 36px rgba(15, 23, 42, 0.18)",
            }}
          >
            <div style={{ fontSize: "13px", fontWeight: 700, color: "var(--text-primary)" }}>
              {t("sendShortcut.title")}
            </div>
            <div style={{ marginTop: "6px", fontSize: "11px", lineHeight: 1.5, color: "var(--text-secondary)" }}>
              {t("sendShortcut.description")}
            </div>
            <button
              type="button"
              autoFocus
              onKeyDown={(event) => {
                event.preventDefault();
                event.stopPropagation();
                if (event.key === "Escape") {
                  setSendShortcutOpen(false);
                  return;
                }
                const next = shortcutFromKeyboardEvent(event.nativeEvent);
                if (next) {
                  setSendShortcutDraft(next);
                  setSendShortcutError("");
                } else if (!["Alt", "AltGraph", "Control", "Meta", "Shift"].includes(event.key)) {
                  setSendShortcutError(t("sendShortcut.modifierRequired"));
                }
              }}
              style={{
                width: "100%",
                minHeight: "48px",
                marginTop: "12px",
                padding: "10px 12px",
                border: "1px solid var(--border-color)",
                borderRadius: "10px",
                background: "var(--content-bg)",
                color: sendShortcutDraft ? "var(--text-primary)" : "var(--text-secondary)",
                fontSize: "13px",
                fontWeight: sendShortcutDraft ? 700 : 500,
                outline: "none",
                cursor: "text",
              }}
            >
              {sendShortcutDraft ? formatSendShortcut(sendShortcutDraft) : t("sendShortcut.pressKeys")}
            </button>
            {sendShortcutError ? (
              <div style={{ marginTop: "8px", color: "#dc2626", fontSize: "11px", lineHeight: 1.4 }}>
                {sendShortcutError}
              </div>
            ) : null}
            <div style={{ ...agentConfigActionRowStyle, marginTop: "12px" }}>
              <button
                type="button"
                onClick={() => {
                  setSendShortcutDraft(null);
                  setSendShortcutError("");
                }}
                style={agentConfigSecondaryButtonStyle(false)}
              >
                {t("sendShortcut.clear")}
              </button>
              <button type="button" onClick={saveSendShortcut} style={agentConfigPrimaryButtonStyle(false)}>
                {t("common.save")}
              </button>
            </div>
          </div>
        ) : null );
}
