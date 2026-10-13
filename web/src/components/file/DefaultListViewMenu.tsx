import type { RefObject } from "react";
import {
  FileOperationItems,
  moreMenuPopoverStyle,
  menuOverlayStyle,
  MoveFilePopover,
} from "./FileOperations";
import { DirectorySortMenu, type DirectorySortControlValue } from "./DirectorySortMenu";
import { GitBranchMenuIcon } from "./listViewIcons";
import type { MessageKey } from "../../i18n/index";

export type DefaultListViewMenuProps = {
  isMenuOpen: boolean;
  setIsMenuOpen: (open: boolean | ((open: boolean) => boolean)) => void;
  moveOpen: boolean;
  setMoveOpen: (open: boolean) => void;
  isSortMenuOpen: boolean;
  setIsSortMenuOpen: (open: boolean | ((open: boolean) => boolean)) => void;
  sortControlValue: DirectorySortControlValue;
  onSortModeChange?: (mode: DirectorySortControlValue) => void;
  sortLabel: (value: DirectorySortControlValue) => string;
  isRootView: boolean;
  root?: string;
  path?: string;
  rootPath?: string;
  rootDisplayName?: string;
  onFileOperationComplete?: () => void | Promise<void>;
  isGitRepo?: boolean;
  isGitWorktree?: boolean;
  enableGitHistoryToggle?: boolean;
  showGitHistory?: boolean;
  onToggleGitHistory?: () => void;
  onCreateWorktree?: () => void;
  onSwitchWorktree?: () => void;
  onRemoveWorktree?: () => void;
  onOpenScheduledAgentTasks?: () => void;
  setRootDraft: (draft: string) => void;
  setEditingRoot: (editing: boolean) => void;
  onRemoveRoot?: () => void;
  onCreateBlankFile?: () => void | Promise<void>;
  onUploadFiles?: (files: File[]) => void | Promise<void>;
  inputRef: RefObject<HTMLInputElement | null>;
  menuOverlay?: React.ReactNode;
  t: (key: MessageKey) => string;
};

export function DefaultListViewMenu(props: DefaultListViewMenuProps) {
  const {
    isMenuOpen,
    setIsMenuOpen,
    moveOpen,
    setMoveOpen,
    isSortMenuOpen,
    setIsSortMenuOpen,
    sortControlValue,
    onSortModeChange,
    sortLabel,
    isRootView,
    root,
    path,
    rootPath,
    rootDisplayName,
    onFileOperationComplete,
    isGitRepo,
    isGitWorktree,
    enableGitHistoryToggle,
    showGitHistory,
    onToggleGitHistory,
    onCreateWorktree,
    onSwitchWorktree,
    onRemoveWorktree,
    onOpenScheduledAgentTasks,
    setRootDraft,
    setEditingRoot,
    onRemoveRoot,
    onCreateBlankFile,
    onUploadFiles,
    inputRef,
    menuOverlay,
    t,
  } = props;
  return (
    <>
      {isMenuOpen ? (
      <div
        style={moreMenuPopoverStyle}
      >
        <DirectorySortMenu
          isOpen={isSortMenuOpen}
          setIsOpen={setIsSortMenuOpen}
          sortControlValue={sortControlValue}
          onSortModeChange={onSortModeChange}
          sortLabel={sortLabel}
          closeMenu={() => setIsMenuOpen(false)}
        />
        {/* 主视图切换已收敛到左栏底部的 MainViewSwitcher（唯一入口），此处不再显示模式字样 */}
        <div
          style={{
            height: "1px",
            background: "var(--border-color)",
            margin: "6px 4px",
          }}
        />
        {!isRootView && root && path && <FileOperationItems key={`${root}:${path}`} root={root} path={path} onMove={() => { setIsMenuOpen(false); setMoveOpen(true); }} onComplete={async () => { await onFileOperationComplete?.(); setIsMenuOpen(false); }} />}
        {isRootView ? (
          <>
            {isGitRepo && enableGitHistoryToggle ? (
              <button
                type="button"
                onClick={() => {
                  onToggleGitHistory?.();
                }}
                style={{
                  width: "100%",
                  border: "none",
                  background: showGitHistory
                    ? "var(--selection-bg)"
                    : "transparent",
                  color: showGitHistory
                    ? "var(--accent-color)"
                    : "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: "12px",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "8px",
                    minWidth: 0,
                  }}
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="15"
                    height="15"
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                    style={{ flexShrink: 0 }}
                  >
                    <path
                      fill="none"
                      stroke="currentColor"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth="2"
                      d="M4.266 16.06a8.92 8.92 0 0 0 3.915 3.978a8.7 8.7 0 0 0 5.471.832a8.8 8.8 0 0 0 4.887-2.64a9.07 9.07 0 0 0 2.388-5.079a9.14 9.14 0 0 0-1.044-5.53a8.9 8.9 0 0 0-4.069-3.815a8.7 8.7 0 0 0-5.5-.608c-1.85.401-3.366 1.313-4.62 2.755c-.151.16-.735.806-1.22 1.781M7.5 8l-3.609.72L3 5m9 4v4l3 2"
                    />
                  </svg>
                  <span>{t("directory.gitHistory")}</span>
                </span>
                <span
                  style={{
                    fontSize: "11px",
                    opacity: showGitHistory ? 1 : 0,
                  }}
                >
                  ✓
                </span>
              </button>
            ) : null}
            {isGitRepo ? (
              <button
                type="button"
                onClick={() => {
                  onCreateWorktree?.();
                  setIsMenuOpen(false);
                }}
                style={{
                  width: "100%",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <GitBranchMenuIcon marker="plus" />
                <span>{t("directory.createWorktree")}</span>
              </button>
            ) : null}
            {isGitRepo ? (
              <button
                type="button"
                onClick={() => {
                  onSwitchWorktree?.();
                  setIsMenuOpen(false);
                }}
                style={{
                  width: "100%",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <GitBranchMenuIcon marker="switch" />
                <span>{t("directory.switchWorktree")}</span>
              </button>
            ) : null}
            {isGitWorktree ? (
              <button
                type="button"
                onClick={() => {
                  onRemoveWorktree?.();
                  setIsMenuOpen(false);
                }}
                style={{
                  width: "100%",
                  border: "none",
                  background: "transparent",
                  color: "#dc2626",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <GitBranchMenuIcon marker="minus" />
                <span>{t("directory.removeWorktree")}</span>
              </button>
            ) : null}
            {isGitRepo || isGitWorktree ? (
              <div
                style={{
                  height: "1px",
                  background: "var(--border-color)",
                  margin: "6px 4px",
                }}
              />
            ) : null}
            <button
              type="button"
              onClick={() => {
                onOpenScheduledAgentTasks?.();
                setIsMenuOpen(false);
              }}
              style={{
                width: "100%",
                border: "none",
                background: "transparent",
                color: "var(--text-primary)",
                borderRadius: "8px",
                padding: "8px 10px",
                display: "flex",
                alignItems: "center",
                gap: "8px",
                textAlign: "left",
                cursor: "pointer",
                fontSize: "12px",
              }}
            >
              <svg
                width="14"
                height="14"
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
              <span>{t("directory.scheduledTasks")}</span>
            </button>
            <button
              type="button"
              onClick={() => {
                setRootDraft(rootDisplayName || root || "");
                setEditingRoot(true);
                setIsMenuOpen(false);
              }}
              style={{
                width: "100%",
                border: "none",
                background: "transparent",
                color: "var(--text-primary)",
                borderRadius: "8px",
                padding: "8px 10px",
                display: "flex",
                alignItems: "center",
                gap: "8px",
                textAlign: "left",
                cursor: "pointer",
                fontSize: "12px",
              }}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M12 20h9" />
                <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
              </svg>
              <span>{t("directory.renameProject")}</span>
            </button>
            <button
              type="button"
              onClick={() => {
                onRemoveRoot?.();
                setIsMenuOpen(false);
              }}
              style={{
                width: "100%",
                border: "none",
                background: "transparent",
                color: "#dc2626",
                borderRadius: "8px",
                padding: "8px 10px",
                display: "flex",
                alignItems: "center",
                gap: "8px",
                textAlign: "left",
                cursor: "pointer",
                fontSize: "12px",
              }}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5.2a2 2 0 0 1 1.4.58l1.82 1.84A2 2 0 0 0 13.84 6H20a2 2 0 0 1 2 2z" />
                <path d="M9 14h6" />
              </svg>
              <span>{t("directory.removeProject")}</span>
            </button>
            <div
              style={{
                height: "1px",
                background: "var(--border-color)",
                margin: "6px 4px",
              }}
            />
          </>
        ) : null}
        <button
          type="button"
          disabled={!root || !onCreateBlankFile}
          onClick={() => {
            setIsMenuOpen(false);
            void onCreateBlankFile?.();
          }}
          style={{ width: "100%", border: "none", background: "transparent", color: "var(--text-primary)", borderRadius: "8px", padding: "8px 10px", display: "flex", alignItems: "center", gap: "8px", textAlign: "left", cursor: root ? "pointer" : "not-allowed", fontSize: "12px", opacity: root ? 1 : 0.45 }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
            <path d="M14 2v6h6M12 12v6M9 15h6" />
          </svg>
          <span>{t("directory.createBlankFile")}</span>
        </button>
        <button
          type="button"
          onClick={() => {
            inputRef.current?.click();
            setIsMenuOpen(false);
          }}
          disabled={!root}
          style={{
            width: "100%",
            border: "none",
            background: "transparent",
            color: root
              ? "var(--text-primary)"
              : "var(--text-secondary)",
            borderRadius: "8px",
            padding: "8px 10px",
            display: "flex",
            alignItems: "center",
            gap: "8px",
            textAlign: "left",
            cursor: root ? "pointer" : "not-allowed",
            fontSize: "12px",
            opacity: root ? 1 : 0.45,
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
          >
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
          <span>{t("directory.uploadHere")}</span>
        </button>
      </div>
    ) : null}
    {moveOpen && root && path ? <div style={menuOverlayStyle}><MoveFilePopover root={root} rootPath={rootPath} path={path} onComplete={async () => { setMoveOpen(false); await onFileOperationComplete?.(); }} /></div> : null}
    {menuOverlay ? (
      <div
        style={menuOverlayStyle}
      >
        {menuOverlay}
      </div>
    ) : null}
    <input
    ref={inputRef}
    type="file"
    multiple
    style={{ display: "none" }}
    onChange={(event) => {
      const files = Array.from(event.target.files || []);
      if (files.length > 0) {
        void onUploadFiles?.(files);
      }
      event.currentTarget.value = "";
    }}
  />
    </>
  );
}
