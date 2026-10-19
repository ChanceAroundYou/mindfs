import React from "react";
import { useI18n } from "../../i18n/index";

/**
 * 会话标题栏里的「用户消息摘要 / 对话历史」按钮。
 *
 * 原本是 SessionViewer 右下角的浮球（SessionViewerFloatingControls），遮挡正文；
 * 现在搬到 header，紧贴 ⋯ 菜单左侧。图标换成钟表；popup 改为向下展开。
 *
 * 状态仍由 useUserMessageSummaries 持有（hover/pinned/open/索引/refs），
 * 这里只负责接线与渲染，逻辑零变化。
 */
export function SessionHistoryMenu({
  userMessageSummaries,
  userSummaryOpen,
  userSummaryRootRef,
  userSummaryListRef,
  currentUserMessageIndex,
  refreshCurrentUserMessageIndex,
  scrollToUserMessageSummary,
  setUserSummaryHoverOpen,
  setUserSummaryPinnedOpen,
  windowMeta,
  visibleExchanges,
  themeColor,
}: any) {
  const { t } = useI18n();
  const themeMuted = `color-mix(in srgb, ${themeColor} 56%, #94a3b8)`;
  const themeMutedBorder = `color-mix(in srgb, ${themeColor} 20%, transparent)`;

  return (
    <div
      ref={userSummaryRootRef}
      onMouseEnter={() => {
        refreshCurrentUserMessageIndex();
        setUserSummaryHoverOpen(true);
      }}
      onMouseLeave={() => setUserSummaryHoverOpen(false)}
      style={{ position: "relative", flexShrink: 0 }}
    >
      {userSummaryOpen ? (
        <>
          <div
            aria-hidden="true"
            style={{
              position: "absolute",
              right: 0,
              top: "100%",
              width: "min(320px, calc(100vw - 72px))",
              height: "8px",
            }}
          />
          <div
            ref={userSummaryListRef}
            role="dialog"
            aria-label={t("session.userSummary")}
            style={{
              position: "absolute",
              right: 0,
              top: "calc(100% + 8px)",
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
              zIndex: 20,
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
            <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
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
        aria-label={
          userSummaryOpen
            ? t("session.hideUserSummary")
            : t("session.showUserSummary")
        }
        title={
          userSummaryOpen
            ? t("session.hideUserSummary")
            : t("session.showUserSummary")
        }
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: userSummaryOpen
            ? `1px solid ${themeMuted}`
            : `1px solid ${themeMutedBorder}`,
          background: userSummaryOpen ? themeMuted : "var(--menu-bg)",
          color: userSummaryOpen ? "#ffffff" : themeMuted,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          outline: "none",
          position: "relative",
        }}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7v5l3 2" />
        </svg>
        <span
          aria-hidden="true"
          style={{
            position: "absolute",
            top: "-4px",
            right: "-4px",
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
          {userMessageSummaries.length > 99
            ? "99+"
            : userMessageSummaries.length}
        </span>
      </button>
    </div>
  );
}
