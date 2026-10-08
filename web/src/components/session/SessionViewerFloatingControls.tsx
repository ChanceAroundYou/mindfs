import React from "react";
import { UserMessageListIcon } from "./SessionViewerCards";

export function SessionViewerFloatingControls(props: any) {
  const { userMessageSummaries, showJumpToLatest, sessionKey, composerOverlayInset, cancelTargetSeqScroll, targetSeq, targetSeqScrollKeyRef, targetSeqRequestKey, shouldStickToBottomRef, setShowJumpToLatest, stickSessionToBottom, t, themeColor, userSummaryRootRef, refreshCurrentUserMessageIndex, setUserSummaryHoverOpen, userSummaryOpen, windowMeta, visibleExchanges, userSummaryListRef, currentUserMessageIndex, scrollToUserMessageSummary, setUserSummaryPinnedOpen, themeMuted, themeMutedBorder } = props;
  return (userMessageSummaries.length > 0 || showJumpToLatest ? (
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
            {showJumpToLatest ? (
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
            ) : null}
            {userMessageSummaries.length > 0 ? (
              <div
                ref={userSummaryRootRef}
                onMouseEnter={() => {
                  refreshCurrentUserMessageIndex();
                  setUserSummaryHoverOpen(true);
                }}
                onMouseLeave={() => setUserSummaryHoverOpen(false)}
                style={{
                  position: "relative",
                  display: "inline-flex",
                }}
              >
                {userSummaryOpen ? (
                  <>
                    <div
                      aria-hidden="true"
                      style={{
                        position: "absolute",
                        right: 0,
                        bottom: "100%",
                        width: "min(320px, calc(100vw - 72px))",
                        height: "8px",
                      }}
                    />
                    <div
                      role="dialog"
                      aria-label={t("session.userSummary")}
                      style={{
                        position: "absolute",
                        right: 0,
                        bottom: "calc(100% + 8px)",
                        width: "min(320px, calc(100vw - 72px))",
                        maxHeight: "260px",
                        overflowY: "auto",
                        padding: "6px",
                        borderRadius: "8px",
                        border: "1px solid var(--menu-border)",
                        background: "var(--menu-bg)",
                        boxShadow: "0 16px 34px rgba(15, 23, 42, 0.18)",
                        color: "var(--text-primary)",
                        boxSizing: "border-box",
                      }}
                    >
                      {windowMeta.total > 0 ? (
                        <div
                          style={{
                            fontSize: "11px",
                            lineHeight: "16px",
                            color: "var(--text-secondary)",
                            padding: "2px 8px 6px",
                            borderBottom: "1px solid var(--menu-border)",
                          }}
                        >
                          已加载 {visibleExchanges.length} / 共 {windowMeta.total}
                        </div>
                      ) : null}
                      <div
                        ref={userSummaryListRef}
                        style={{ display: "flex", flexDirection: "column", gap: "2px" }}
                      >
                        {userMessageSummaries.map((item: any) => {
                          const isCurrent = item.index === currentUserMessageIndex;
                          return (
                            <button
                              key={item.id}
                              type="button"
                              data-user-summary-index={item.index}
                              onClick={() => scrollToUserMessageSummary(item.index)}
                              style={{
                                width: "100%",
                                border: "none",
                                background: isCurrent
                                  ? "rgba(148, 163, 184, 0.22)"
                                  : "transparent",
                                display: "block",
                                padding: "6px 8px",
                                borderRadius: "6px",
                                cursor: "pointer",
                                textAlign: "left",
                                color: "var(--text-primary)",
                              }}
                              onMouseEnter={(event) => {
                                event.currentTarget.style.background = "var(--menu-active-bg)";
                              }}
                              onMouseLeave={(event) => {
                                event.currentTarget.style.background = isCurrent
                                  ? "rgba(148, 163, 184, 0.22)"
                                  : "transparent";
                              }}
                            >
                              <span
                                title={item.summary}
                                style={{
                                  display: "block",
                                  minWidth: 0,
                                  fontSize: "12px",
                                  lineHeight: "18px",
                                  color: "var(--text-primary)",
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                  whiteSpace: "nowrap",
                                }}
                              >
                                {item.summary}
                              </span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  </>
                ) : null}
                <button
                  type="button"
                  onClick={() => {
                    refreshCurrentUserMessageIndex();
                    setUserSummaryPinnedOpen((open: boolean) => {
                      const nextOpen = !open;
                      if (!nextOpen) {
                        setUserSummaryHoverOpen(false);
                      }
                      return nextOpen;
                    });
                  }}
                  aria-label={userSummaryOpen ? t("session.hideUserSummary") : t("session.showUserSummary")}
                  title={userSummaryOpen ? t("session.hideUserSummary") : t("session.showUserSummary")}
                  style={{
                    position: "relative",
                    width: "34px",
                    height: "34px",
                    border: userSummaryOpen ? `1px solid ${themeMuted}` : `1px solid ${themeMutedBorder}`,
                    borderRadius: "8px",
                    background: userSummaryOpen ? themeMuted : "var(--menu-bg)",
                    color: userSummaryOpen ? "#ffffff" : themeMuted,
                    boxShadow: "0 10px 24px rgba(15, 23, 42, 0.16)",
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    cursor: "pointer",
                    fontSize: "18px",
                  }}
                >
                  <UserMessageListIcon />
                  <span
                    aria-hidden="true"
                    style={{
                      position: "absolute",
                      top: "-7px",
                      right: "-7px",
                      minWidth: "18px",
                      height: "18px",
                      padding: "0 5px",
                      borderRadius: "999px",
                      background: themeColor,
                      color: "#ffffff",
                      border: "2px solid var(--menu-bg)",
                      fontSize: "10px",
                      fontWeight: 800,
                      lineHeight: "14px",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      boxSizing: "border-box",
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {userMessageSummaries.length > 99 ? "99+" : userMessageSummaries.length}
                  </span>
                </button>
              </div>
            ) : null}
          </div>
        ) : null );
}
