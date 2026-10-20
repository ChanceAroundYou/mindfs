import React from "react";
import type { DirectorySortMode, FileEntry } from "../../shared/directorySort";
import { FileEntryIcon, formatCompactSize, formatCompactTime } from "./listViewIcons";
import { FileEntryMenu } from "./FileEntryMenu";

/**
 * 文件浏览器列表的一行：图标 + 名称 + 紧凑元信息 + 右侧三点操作菜单。
 *
 * 从 DefaultListView 抽出：行本身是纯展示，三点菜单是唯一的交互入口。
 */
export type FileEntryRowProps = {
  entry: FileEntry;
  sortMode: DirectorySortMode;
  /** 排序按时间/大小时，右侧多一列紧凑元信息。 */
  showCompactMeta: boolean;
  onItemClick?: (entry: FileEntry) => void;
  /** 项目 id：有项目才有可操作的文件（工作台态没有）。 */
  root?: string;
  /** 项目绝对路径：移动面板浏览目标目录时要用。 */
  rootPath?: string;
  /** 文件操作成功后刷新列表。 */
  onFileOperationComplete?: () => void | Promise<void>;
};

export function FileEntryRow({
  entry,
  sortMode,
  showCompactMeta,
  onItemClick,
  root,
  rootPath,
  onFileOperationComplete,
}: FileEntryRowProps) {
  return (
    <div
      onClick={() => onItemClick?.(entry)}
      style={{
        background: "transparent",
        border: "1px solid transparent",
        borderRadius: "8px",
        padding: "6px 10px",
        display: "flex",
        alignItems: "center",
        gap: "8px",
        transition: "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)",
        cursor: "pointer",
        transform: "translateZ(0)",
        willChange: "background-color",
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = "rgba(0, 0, 0, 0.03)";
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = "transparent";
      }}
    >
      <FileEntryIcon entry={entry} />
      <div
        style={{
          minWidth: 0,
          flex: 1,
          fontWeight: 500,
          fontSize: "13px",
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          color: "var(--text-primary)",
        }}
      >
        {entry.name}
      </div>
      {showCompactMeta ? (
        <div
          style={{
            flexShrink: 0,
            minWidth: "76px",
            textAlign: "right",
            fontSize: "11px",
            color: "var(--text-secondary)",
            opacity: 0.88,
            fontVariantNumeric: "tabular-nums",
          }}
        >
          {sortMode === "mtime-desc" || sortMode === "mtime-asc"
            ? formatCompactTime(entry.mtime)
            : !entry.is_dir
              ? formatCompactSize(entry.size)
              : ""}
        </div>
      ) : null}
      {root ? (
        <FileEntryMenu
          root={root}
          rootPath={rootPath}
          path={entry.path}
          onComplete={onFileOperationComplete ?? (() => {})}
        />
      ) : null}
    </div>
  );
}
