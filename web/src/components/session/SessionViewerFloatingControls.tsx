import React from "react";
import { ScrollToBottomButton } from "../common/ScrollToBottomButton";

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
            <ScrollToBottomButton
              label={t("session.jumpLatest")}
              onClick={() => {
                cancelTargetSeqScroll();
                if (targetSeq) {
                  targetSeqScrollKeyRef.current = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
                }
                shouldStickToBottomRef.current = true;
                setShowJumpToLatest(false);
                stickSessionToBottom("smooth");
              }}
              themeColor={themeColor}
            />
          </div>
        ) : null );
}
