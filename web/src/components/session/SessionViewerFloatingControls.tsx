import React from "react";

export function SessionViewerFloatingControls(props: any) {
  const { showJumpToLatest, sessionKey, composerOverlayInset, cancelTargetSeqScroll, targetSeq, targetSeqScrollKeyRef, targetSeqRequestKey, shouldStickToBottomRef, setShowJumpToLatest, stickSessionToBottom, t, themeColor } = props;
  return (showJumpToLatest ? (
          <div
            style={{
              position: "absolute",
              left: "50%",
              transform: "translateX(-50%)",
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
                justifyContent: "center",
                width: "34px",
                height: "34px",
                border: `1px solid color-mix(in srgb, ${themeColor} 35%, transparent)`,
                background: themeColor,
                color: "#ffffff",
                borderRadius: "999px",
                boxShadow: "0 8px 24px rgba(15, 23, 42, 0.14)",
                cursor: "pointer",
                fontSize: "12px",
                whiteSpace: "nowrap",
              }}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M12 5v14" />
                <path d="m19 12-7 7-7-7" />
              </svg>
            </button>
          </div>
        ) : null );
}
