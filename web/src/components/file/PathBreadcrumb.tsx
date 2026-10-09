import React from "react";
import { useI18n } from "../../i18n/index";
import type { LocalDirItem } from "./ProjectAddPopover";

export function PathBreadcrumb({
  path,
  volumes,
  onNavigate,
}: {
  path: string;
  volumes?: LocalDirItem[];
  onNavigate: (path: string) => void;
}) {
  const { t } = useI18n();
  const isWindowsPath = /^[A-Za-z]:[\\/]/.test(path);
  const normalized = path.replace(/\\/g, "/").replace(/\/+$/, "");
  const segments = normalized.split("/").filter(Boolean);
  const [volumeMenuOpen, setVolumeMenuOpen] = React.useState(false);
  if (segments.length === 0) {
    // Posix root ("/"): keep a clickable anchor so the browser can navigate back down.
    if (isWindowsPath || !path.startsWith("/")) {
      return null;
    }
    return (
      <button
        type="button"
        onClick={() => onNavigate("/")}
        style={{
          border: "none",
          background: "transparent",
          padding: 0,
          color: "var(--text-primary)",
          fontSize: "13px",
          fontWeight: 600,
          cursor: "pointer",
        }}
      >
        /
      </button>
    );
  }
  const volumeItems = Array.isArray(volumes) ? volumes : [];
  const visibleSegments =
    segments.length > 3
      ? segments.slice(segments.length - 3)
      : segments;
  const hiddenCount = segments.length - visibleSegments.length;
  const normalizedPath = path.toLowerCase().replace(/\//g, "\\");
  const activeVolume = volumeItems.find((volume) =>
    normalizedPath.startsWith(volume.path.toLowerCase().replace(/\//g, "\\")),
  );
  const pathForSegment = (index: number): string => {
    if (isWindowsPath) {
      return index === 0
        ? `${segments[0]}\\`
        : `${segments[0]}\\${segments.slice(1, index + 1).join("\\")}`;
    }
    return `/${segments.slice(0, index + 1).join("/")}`;
  };
  const renderVolumeButton = (isLast: boolean) => (
    <div style={{ position: "relative", display: "inline-flex", alignItems: "center", gap: "1px" }}>
      <button
        type="button"
        onClick={() => onNavigate(activeVolume?.path || pathForSegment(0))}
        style={{
          border: "none",
          background: "transparent",
          padding: 0,
          color: isLast ? "var(--text-primary)" : "var(--text-secondary)",
          fontSize: isLast ? "13px" : "11px",
          fontWeight: isLast ? 600 : 500,
          cursor: "pointer",
          whiteSpace: "nowrap",
        }}
      >
        {activeVolume?.name || segments[0]}
      </button>
      <button
        type="button"
        onClick={() => setVolumeMenuOpen((open) => !open)}
        style={{
          border: "none",
          background: "transparent",
          padding: 0,
          color: isLast ? "var(--text-primary)" : "var(--text-secondary)",
          cursor: "pointer",
          display: "inline-flex",
          alignItems: "center",
        }}
        aria-label={t("projectAdd.selectVolume")}
      >
        <svg
          width="11"
          height="11"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{
            transform: volumeMenuOpen ? "rotate(180deg)" : "rotate(0deg)",
            transition: "transform 120ms ease",
          }}
        >
          <polyline points="6 9 12 15 18 9" />
        </svg>
      </button>
      {volumeMenuOpen ? (
        <div
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            left: 0,
            zIndex: 30,
            minWidth: "76px",
            padding: "4px",
            border: "1px solid var(--border-color)",
            borderRadius: "8px",
            background: "var(--menu-bg)",
            boxShadow: "0 10px 24px rgba(15, 23, 42, 0.14)",
            display: "flex",
            flexDirection: "column",
            gap: "2px",
          }}
        >
          {volumeItems.map((volume) => {
            const active = activeVolume?.path === volume.path;
            return (
              <button
                key={volume.path}
                type="button"
                onClick={() => {
                  setVolumeMenuOpen(false);
                  onNavigate(volume.path);
                }}
                style={{
                  border: "none",
                  background: active ? "var(--selection-bg)" : "transparent",
                  color: active ? "var(--accent-color)" : "var(--text-primary)",
                  borderRadius: "6px",
                  padding: "6px 8px",
                  fontSize: "12px",
                  fontWeight: active ? 600 : 500,
                  cursor: "pointer",
                  textAlign: "left",
                }}
              >
                {volume.name}
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "4px",
        flexWrap: "nowrap",
        minWidth: 0,
      }}
    >
      {hiddenCount > 0 ? (
        <>
          <button
            type="button"
            onClick={() => onNavigate(pathForSegment(segments.length - 4))}
            style={{
              border: "none",
              background: "transparent",
              padding: 0,
              color: "var(--text-secondary)",
              fontSize: "11px",
              fontWeight: 500,
              cursor: "pointer",
              whiteSpace: "nowrap",
            }}
          >
            ...
          </button>
          <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
            &gt;
          </span>
        </>
      ) : null}
      {visibleSegments.map((segment, index) => {
        const absoluteIndex = hiddenCount + index;
        const segmentPath = pathForSegment(absoluteIndex);
        const isLast = index === visibleSegments.length - 1;
        return (
          <React.Fragment key={segmentPath}>
            {isWindowsPath && absoluteIndex === 0 && volumeItems.length > 0 ? (
              renderVolumeButton(isLast)
            ) : (
              <button
                type="button"
                onClick={() => onNavigate(segmentPath)}
                style={{
                  border: "none",
                  background: "transparent",
                  padding: 0,
                  color: isLast ? "var(--text-primary)" : "var(--text-secondary)",
                  fontSize: isLast ? "13px" : "11px",
                  fontWeight: isLast ? 600 : 500,
                  cursor: "pointer",
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {segment}
              </button>
            )}
            {!isLast ? (
              <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
                &gt;
              </span>
            ) : null}
          </React.Fragment>
        );
      })}
    </div>
  );
}
