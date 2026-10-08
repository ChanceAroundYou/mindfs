import React from "react";

export function SessionViewerLoadingState({ loadingMore, loading, hasVisibleTimeline }: { loadingMore: boolean; loading?: boolean; hasVisibleTimeline: boolean }) {
  return (
    <>
      {loadingMore ? (
  <div
    style={{
      display: "flex",
      justifyContent: "center",
      padding: "8px 0",
    }}
  >
    <span
      style={{
        width: "14px",
        height: "14px",
        border: "2px solid var(--border-color)",
        borderTopColor: "var(--accent-color)",
        borderRadius: "50%",
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
