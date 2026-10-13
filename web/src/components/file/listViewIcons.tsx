import { SymlinkBadge } from "./SymlinkBadge";
import type { FileEntry } from "../../services/prefs/directorySort";
import { formatCompactSize, formatCompactTime } from "../../shared/datetime";

// 列表项的行内图标，从 DefaultListView 抽出。
// 紧凑时间/尺寸格式化见 shared/datetime。

export { formatCompactSize, formatCompactTime };

export function FileEntryIcon({ entry }: { entry: FileEntry }) {
  const showSymlinkBadge = entry.is_dir && entry.is_symlink;

  return (
    <div
      style={{
        position: "relative",
        width: "18px",
        height: "18px",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
      }}
    >
      {entry.is_dir ? (
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="var(--accent-color)"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2z" />
        </svg>
      ) : (
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="var(--text-secondary)"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" />
          <polyline points="13 2 13 9 20 9" />
        </svg>
      )}
      {showSymlinkBadge ? <SymlinkBadge /> : null}
    </div>
  );
}

export function GitBranchMenuIcon({
  marker,
}: {
  marker?: "plus" | "minus" | "switch";
}) {
  const markerPath =
    marker === "plus"
      ? "M19 16v6M16 19h6"
      : marker === "minus"
        ? "M16 19h6"
        : "M21 17.5h-7M18 14.5l3 3M13.8 21h7M13.8 21l3 3";
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
      style={{ flexShrink: 0 }}
    >
      <path
        fill="currentColor"
        d="M7 5a2 2 0 1 1 3.763.945h.58a4 4 0 0 1 4 4v1.28a2 2 0 0 1-1.02 3.72a2 2 0 0 1-.98-3.745V9.945a2 2 0 0 0-2-2H10v9.323A2 2 0 0 1 9 21a2 2 0 0 1-1-3.732V6.732A2 2 0 0 1 7 5"
      />
      {marker ? (
        <path
          d={markerPath}
          stroke="currentColor"
          strokeWidth={marker === "switch" ? "1.7" : "2.6"}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      ) : null}
    </svg>
  );
}
