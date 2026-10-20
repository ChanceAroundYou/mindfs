import React from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { FileOperationItems, MoreMenuButton, moreMenuPopoverStyle, menuOverlayStyle, MoveFilePopover } from "./FileOperations";
import { DefaultListViewMenu } from "./DefaultListViewMenu";
import { FileEntryRow } from "./FileEntryRow";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { Breadcrumbs } from "./Breadcrumbs";
import { useResponsive } from "../../hooks/useResponsive";
import {
  DIRECTORY_SORT_OPTIONS,
  type DirectorySortMode,
  type FileEntry,
  sortDirectoryEntries,
} from "../../shared/directorySort";
import type { UploadProgress } from "../../shared/uploadTypes";
import { useI18n, type MessageKey } from "../../i18n/index";
import { CompactUploadProgress } from "./CompactUploadProgress";

export type { DirectorySortControlValue } from "./DirectorySortMenu";
import type { DirectorySortControlValue } from "./DirectorySortMenu";
export type MainContentViewMode = "task-kanban" | "file-browser";

const sortLabelKeys: Partial<Record<DirectorySortControlValue, MessageKey>> = {
  "name-asc": "sort.nameAsc",
  "name-desc": "sort.nameDesc",
  "mtime-desc": "sort.mtimeDesc",
  "mtime-asc": "sort.mtimeAsc",
  "size-desc": "sort.sizeDesc",
  "size-asc": "sort.sizeAsc",
};

type DefaultListViewProps = {
  rootPath?: string;
  onFileOperationComplete?: () => void | Promise<void>;
  root?: string;
  rootDisplayName?: string;
  path?: string;
  entries: FileEntry[];
  errorMessage?: string;
  topContent?: React.ReactNode;
  showHiddenFiles?: boolean;
  sortMode: DirectorySortMode;
  sortControlValue: DirectorySortControlValue;
  onItemClick?: (entry: FileEntry) => void;
  onPathClick?: (path: string) => void;
  onSortModeChange?: (mode: DirectorySortControlValue) => void;
  onCreateBlankFile?: () => void | Promise<void>;
  onUploadFiles?: (files: File[]) => void | Promise<void>;
  onCancelUpload?: () => void;
  uploadProgress?: UploadProgress | null;
  onRenameRoot?: (nextName: string) => Promise<boolean> | boolean;
  onRemoveRoot?: () => void;
  isGitRepo?: boolean;
  isGitWorktree?: boolean;
  showGitHistory?: boolean;
  enableGitHistoryToggle?: boolean;
  onToggleGitHistory?: () => void;
  onCreateWorktree?: () => void;
  onSwitchWorktree?: () => void;
  onRemoveWorktree?: () => void;
  onOpenScheduledAgentTasks?: () => void;
  currentViewMode?: MainContentViewMode;
  /** 跨项目工作台态：顶部不显示项目名面包屑（工作台不属于任何项目），改显示视图名 */
  workspaceMode?: boolean;
  /** 配合 workspaceMode，显示「N 个项目」 */
  workspaceProjectCount?: number;
  menuOverlay?: React.ReactNode;
  rootColor?: string | null;
};

export function DefaultListView({
  root,
  rootDisplayName,
  path = "",
  entries,
  errorMessage,
  topContent,
  showHiddenFiles = false,
  sortMode,
  sortControlValue,
  onItemClick,
  onPathClick,
  onSortModeChange,
  onUploadFiles,
  onCreateBlankFile,
  onCancelUpload,
  uploadProgress = null,
  onRenameRoot,
  onRemoveRoot,
  onFileOperationComplete,
  rootPath,
  isGitRepo = false,
  isGitWorktree = false,
  showGitHistory = true,
  enableGitHistoryToggle = true,
  onToggleGitHistory,
  onCreateWorktree,
  onSwitchWorktree,
  onRemoveWorktree,
  onOpenScheduledAgentTasks,
  currentViewMode = "task-kanban",
  workspaceMode = false,
  workspaceProjectCount = 0,
  menuOverlay = null,
  rootColor = null,
}: DefaultListViewProps) {
  const { t } = useI18n();
  const inputRef = React.useRef<HTMLInputElement>(null);
  const rootNameInputRef = React.useRef<HTMLInputElement>(null);
  const menuRef = React.useRef<HTMLDivElement | null>(null);
  const [isMenuOpen, setIsMenuOpen] = React.useState(false);
  const [moveOpen, setMoveOpen] = React.useState(false);
  React.useEffect(() => { setMoveOpen(false); }, [root, path]);
  const [isSortMenuOpen, setIsSortMenuOpen] = React.useState(false);
  const [editingRoot, setEditingRoot] = React.useState(false);
  const [rootDraft, setRootDraft] = React.useState(rootDisplayName || root || "");
  const [rootRenaming, setRootRenaming] = React.useState(false);
  const sortedEntries = React.useMemo(() => {
    const visibleEntries = showHiddenFiles
      ? entries
      : entries.filter((entry) => !entry.name.startsWith("."));
    return sortDirectoryEntries(visibleEntries, sortMode);
  }, [entries, showHiddenFiles, sortMode]);
  const showCompactMeta =
    sortMode === "mtime-desc" ||
    sortMode === "mtime-asc" ||
    sortMode === "size-desc" ||
    sortMode === "size-asc";
  const isRootView = !!root && (!path || path === root || path === ".");
  const showTaskKanban = currentViewMode === "task-kanban";
  const showFileBrowser = currentViewMode === "file-browser";
  // 看板/工作台的内边距跟着设备走：移动端底部贴着输入区，再留 24px 会多出一道
  // 永远用不上的空带；顶部反而要加大（8px → 16px）才和左右侧的 16px 对齐。
  const { isMobile } = useResponsive();
  const sortLabel = (value: DirectorySortControlValue): string => {
    if (value === "inherit") return t("directory.followGlobal");
    const key = sortLabelKeys[value];
    return key ? t(key) : t("directory.defaultSort");
  };

  React.useEffect(() => {
    if (!editingRoot) {
      setRootDraft(rootDisplayName || root || "");
    }
  }, [editingRoot, root, rootDisplayName]);

  React.useEffect(() => {
    if (!editingRoot) {
      return;
    }
    rootNameInputRef.current?.focus();
    rootNameInputRef.current?.select();
  }, [editingRoot]);

  React.useEffect(() => {
    if (!isMenuOpen && !moveOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setIsMenuOpen(false);
        setMoveOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [isMenuOpen, moveOpen]);

  const cancelRootRename = React.useCallback(() => {
    setEditingRoot(false);
    setRootRenaming(false);
    setRootDraft(rootDisplayName || root || "");
  }, [root, rootDisplayName]);

  const submitRootRename = React.useCallback(async () => {
    if (rootRenaming) {
      return;
    }
    const trimmed = rootDraft.trim();
    if (!trimmed || trimmed === String(rootDisplayName || root || "").trim()) {
      cancelRootRename();
      return;
    }
    if (!onRenameRoot) {
      cancelRootRename();
      return;
    }
    setRootRenaming(true);
    try {
      const ok = await onRenameRoot(trimmed);
      if (ok === false) {
        rootNameInputRef.current?.focus();
        rootNameInputRef.current?.select();
        return;
      }
      setEditingRoot(false);
    } finally {
      setRootRenaming(false);
    }
  }, [cancelRootRename, onRenameRoot, root, rootDisplayName, rootDraft, rootRenaming]);

  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        background: "transparent",
      }}
    >
      <ViewHeader padding="0 3px 0 16px" style={{ zIndex: 10 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            overflow: "hidden",
            flex: 1,
          }}
        >
          {/* 跨项目工作台不属于任何项目，面包屑（项目名 + 路径）在它是错的：
              顶部写着当前选中的项目名，人会以为自己正看着那个项目。
              换成视图名，和 MainViewSwitcher 的「工作台/看板/文件/对话」对齐。 */}
          {workspaceMode ? (
            <div style={{ display: "flex", alignItems: "baseline", gap: "8px", minWidth: 0 }}>
              <span style={{ fontSize: "13px", fontWeight: 800, color: "var(--text-color)", whiteSpace: "nowrap" }}>
                {t("view.workspace")}
              </span>
              <span style={{ fontSize: "11px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
                {t("task.workspaceProjectCount", { count: workspaceProjectCount })}
              </span>
            </div>
          ) : (
          <Breadcrumbs
            root={root}
            rootDisplayName={rootDisplayName || root}
            path={path || ""}
            onPathClick={onPathClick}
            rootColor={rootColor}
            editingRoot={editingRoot}
            rootDraft={rootDraft}
            rootRenaming={rootRenaming}
            inputRef={rootNameInputRef}
            onRootDraftChange={setRootDraft}
            onRootRenameSubmit={() => {
              void submitRootRename();
            }}
            onRootRenameCancel={cancelRootRename}
          />
          )}
          {!workspaceMode && uploadProgress ? (
          <div style={{ marginLeft: "10px", flexShrink: 0 }}>
            <CompactUploadProgress
              progress={uploadProgress}
              label={t("upload.directoryProgress")}
              statusLabel={t("upload.inProgress")}
              cancelLabel={t("upload.cancel")}
              onCancel={onCancelUpload}
            />
          </div>
          ) : null}
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "10px",
            flexShrink: 0,
          }}
        >
          {showFileBrowser ? (
            <div
              style={{
                fontSize: "11px",
                color: "var(--text-secondary)",
                opacity: 0.6,
              }}
            >
              {t("directory.itemCount", { count: sortedEntries.length })}
            </div>
          ) : null}
          <div ref={menuRef} style={{ position: "relative" }} onKeyDown={event => { if (event.key === "Escape") { setIsMenuOpen(false); setMoveOpen(false); } }}>
            <MoreMenuButton
              open={isMenuOpen}
              label={t("directory.openMenu")}
              onboarding="main-menu"
              onClick={() => {
                setMoveOpen(false);
                setIsMenuOpen((open) => {
                  const nextOpen = !open;
                  if (nextOpen) {
                    setIsSortMenuOpen(false);
                  }
                  return nextOpen;
                });
              }}
            />
            <DefaultListViewMenu
              isMenuOpen={isMenuOpen}
              setIsMenuOpen={setIsMenuOpen}
              moveOpen={moveOpen}
              setMoveOpen={setMoveOpen}
              isSortMenuOpen={isSortMenuOpen}
              setIsSortMenuOpen={setIsSortMenuOpen}
              sortControlValue={sortControlValue}
              onSortModeChange={onSortModeChange}
              sortLabel={sortLabel}
              isRootView={isRootView}
              root={root}
              path={path}
              rootPath={rootPath}
              rootDisplayName={rootDisplayName}
              onFileOperationComplete={onFileOperationComplete}
              isGitRepo={isGitRepo}
              isGitWorktree={isGitWorktree}
              enableGitHistoryToggle={enableGitHistoryToggle}
              showGitHistory={showGitHistory}
              onToggleGitHistory={onToggleGitHistory}
              onCreateWorktree={onCreateWorktree}
              onSwitchWorktree={onSwitchWorktree}
              onRemoveWorktree={onRemoveWorktree}
              onOpenScheduledAgentTasks={onOpenScheduledAgentTasks}
              setRootDraft={setRootDraft}
              setEditingRoot={setEditingRoot}
              onRemoveRoot={onRemoveRoot}
              onCreateBlankFile={onCreateBlankFile}
              onUploadFiles={onUploadFiles}
              inputRef={inputRef}
              menuOverlay={menuOverlay}
              t={t}
            />
          </div>
        </div>
      </ViewHeader>

      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {showTaskKanban && topContent ? (
          /* 看板/工作台靠 flex 链铺满主区，这一层是链子的起点。
             必须是**确定高度**（height）而不是 minHeight：子级是 grid，
             行高用百分比（移动端 50% 切两行）。minHeight 只给下限、
             本身是 auto，百分比在 auto 高度里解析不出来 —— 行高退回内容高度，
             四块两行又撑出屏幕。父级是 display:block，所以还得自己声明 flex 列，
             否则子级的 flex:1 完全不生效（它不是 flex item）。
             box-sizing 是全局 border-box，padding 算在这 100% 里面，不会外溢。 */
          <div style={{ padding: isMobile ? "6px 8px 0" : "6px 8px", height: "100%", display: "flex", flexDirection: "column" }}>{topContent}</div>
        ) : null}
        {showFileBrowser ? (
          <div style={{ padding: "24px 16px" }}>
            {errorMessage ? (
              <div
                style={{
                  padding: "18px 16px",
                  borderRadius: "12px",
                  border: "1px solid rgba(245, 158, 11, 0.28)",
                  background: "rgba(245, 158, 11, 0.08)",
                  color: "var(--text-primary)",
                }}
              >
                <div
                  style={{
                    fontSize: "14px",
                    fontWeight: 600,
                    marginBottom: "6px",
                  }}
                >
                  {t("directory.inaccessible")}
                </div>
                <div
                  style={{
                    fontSize: "12px",
                    color: "var(--text-secondary)",
                    lineHeight: 1.5,
                  }}
                >
                  {errorMessage}
                </div>
              </div>
            ) : null}
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "4px",
                width: "100%",
              }}
            >
              {sortedEntries.map((entry) => (
                <FileEntryRow
                  key={entry.path}
                  entry={entry}
                  sortMode={sortMode}
                  showCompactMeta={showCompactMeta}
                  onItemClick={onItemClick}
                  root={root}
                  rootPath={rootPath}
                  onFileOperationComplete={onFileOperationComplete}
                />
              ))}
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}
