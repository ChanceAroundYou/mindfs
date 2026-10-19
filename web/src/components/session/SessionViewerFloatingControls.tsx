import React from "react";

export function SessionViewerFloatingControls(props: any) {
  const { showJumpToLatest, sessionKey, composerOverlayInset, cancelTargetSeqScroll, targetSeq, targetSeqScrollKeyRef, targetSeqRequestKey, shouldStickToBottomRef, setShowJumpToLatest, stickSessionToBottom, t, themeColor } = props;
  return (showJumpToLatest ? (
          <div
            style={{
              position: "absolute",
              right: "16px",
              bottom: `${16 + composerOverlayInset}px`,
              zIndex: 4,
              display: "flex",
              alignItems: "center",
              gap: "6px",
            }}
          >
            <button
              type="button"
              onClick={() => {
                cancelTargetSeqScroll();
                if (targetSeq) {
                  targetSeqScrollKeyRef.current = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
                }
                shouldStickToBottomRef.current = true;
                setShowJumpToLatest(false);
                stickSessionToBottom("smooth");
              }}
              aria-label={t("session.jumpLatest")}
              title={t("session.jumpLatest")}
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "6px",
                height: "34px",
                border: `1px solid color-mix(in srgb, ${themeColor} 35%, transparent)`,
                background: themeColor,
                color: "#ffffff",
                borderRadius: "999px",
                padding: "0 12px",
                boxShadow: "0 8px 24px rgba(15, 23, 42, 0.14)",
                cursor: "pointer",
                fontSize: "12px",
                whiteSpace: "nowrap",
              }}
            >
              <span>{t("session.jumpBottom")}</span>
            </button>
          </div>
        ) : null );
}
