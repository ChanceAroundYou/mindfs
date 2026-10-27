import React from "react";

/**
 * 会话加载状态。
 *
 * `loadingMore`（上翻加载更早的历史）**必须不占布局**：原先它是内容流里的一个
 * padding 8px + 14px 的块，出现时把所有内容整体下推 ~30px、消失时又推回来 ——
 * 前插本身已经在补滚动位置，再叠加这 30px，就是用户看到的「加载时闪一下、位置晃一下」
 * （2026-10-27 真机）。改成 `position: sticky + height: 0` 的浮层：视觉上贴在视口顶部，
 * 盒模型高度为零，出现和消失都不改变任何行的位置。
 *
 * 首屏骨架（`loading && !hasVisibleTimeline`）仍走文档流 —— 那时没有内容可推。
 */
export function SessionViewerLoadingState({
  loadingMore,
  loading,
  hasVisibleTimeline,
  label = "Loading",
}: {
  loadingMore: boolean;
  loading?: boolean;
  hasVisibleTimeline: boolean;
  label?: string;
}) {
  return (
    <>
      {loadingMore ? (
        // height:0 + sticky：不占位，但滚到哪都看得见。pointer-events:none 免得挡住正文点击。
        <div
          style={{
            position: "sticky",
            top: "6px",
            height: 0,
            display: "flex",
            justifyContent: "center",
            pointerEvents: "none",
            zIndex: 3,
          }}
        >
          <span
            role="status"
            aria-label={label}
            data-session-loading-more="1"
            style={{
              width: "18px",
              height: "18px",
              border: "2px solid color-mix(in srgb, var(--accent-color) 30%, transparent)",
              borderTopColor: "var(--accent-color)",
              borderRadius: "50%",
              background: "color-mix(in srgb, var(--menu-bg) 88%, transparent)",
              boxSizing: "border-box",
              animation: "spin 0.8s linear infinite",
            }}
          />
        </div>
      ) : null}
      {loading && !hasVisibleTimeline ? (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "10px",
            padding: "6px 0 8px",
          }}
        >
          {[0, 1, 2].map((index) => (
            <div
              key={index}
              style={{
                width: index === 1 ? "78%" : index === 2 ? "64%" : "52%",
                height: index === 0 ? "18px" : "72px",
                alignSelf:
                  index === 0
                    ? "flex-start"
                    : index === 1
                      ? "flex-end"
                      : "flex-start",
                borderRadius: index === 0 ? "8px" : "18px",
                background:
                  "linear-gradient(90deg, rgba(148,163,184,0.12) 0%, rgba(148,163,184,0.24) 50%, rgba(148,163,184,0.12) 100%)",
                backgroundSize: "200% 100%",
                animation:
                  "sessionLoadingPulse 1.1s ease-in-out infinite",
              }}
            />
          ))}
        </div>
      ) : null}
    </>
  );
}
