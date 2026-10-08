import React from "react";
import type { FileEntry } from "../../services/prefs/directorySort";

export function createRenderEntries(ctx: any) {
  const { ChevronRight, DEFAULT_NODE_COLOR, DirectoryIconSlot, PROJECT_TREE_INDENT, PROJECT_TREE_ROOT_PADDING_LEFT,
    activeSessionKey, childKeyFor, childrenByPath, createInputRef, creatingRootBusy, creatingRootExtraContent,
    creatingRootName, creatingRootSubmitOnBlur, expandedSet, fileMetas, hexToRgbaApp, onCreateRootCancel,
    onCreateRootNameChange, onCreateRootSubmit, onSelectDir, onSelectFile, onSelectRoot, onToggleDir, projectTreeTab,
    renderRootExtraContent, renderRootRelatedContent, renderRootWorktreeContent, rootBadgeStyle, rootId, rootNodeId,
    rootSessionIndicators, scopeKey, selectedDirKey, selectedPath, sortDirectoryEntries, sortMode, t, treeKey, visibleEntries } = ctx;
const renderEntries = (items: FileEntry[], depth: number, branchRoot: string, groupColor?: string, sectionNodeId?: string) => (
  <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
    {depth === 0 && creatingRootName !== null ? (
      <li key="__draft_root__">
        <div
          style={{
            padding: "6px 8px",
            paddingLeft: PROJECT_TREE_ROOT_PADDING_LEFT,
            display: "flex",
            alignItems: creatingRootExtraContent ? "flex-start" : "center",
            gap: "8px",
            width: "100%",
            color: "var(--accent-color)",
            fontSize: "13px",
            borderRadius: "6px",
            background: "var(--selection-bg)",
            boxSizing: "border-box",
          }}
        >
          <div style={{ width: 20, display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0 }}>
            <ChevronRight isOpen={false} />
          </div>
          <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "8px" }}>
            <input
              ref={createInputRef}
              value={creatingRootName}
              disabled={creatingRootBusy}
              onChange={(event) => onCreateRootNameChange?.(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  onCreateRootSubmit?.();
                } else if (event.key === "Escape") {
                  event.preventDefault();
                  onCreateRootCancel?.();
                }
              }}
              onBlur={() => {
                if (!creatingRootBusy && creatingRootSubmitOnBlur) {
                  onCreateRootSubmit?.();
                }
              }}
              style={{
                width: "100%",
                border: "none",
                background: "transparent",
                color: "var(--text-primary)",
                fontSize: "13px",
                fontWeight: 600,
                outline: "none",
                padding: 0,
              }}
            />
            {creatingRootExtraContent}
          </div>
          {!creatingRootExtraContent ? null : (
            <div style={{ display: "flex", alignItems: "center", gap: "4px", flexShrink: 0 }}>
              <button
                type="button"
                disabled={creatingRootBusy}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onCreateRootSubmit?.()}
                style={{
                  border: "none",
                  background: "var(--accent-color)",
                  color: "#fff",
                  borderRadius: "6px",
                  padding: "4px 7px",
                  fontSize: "11px",
                  fontWeight: 600,
                  cursor: creatingRootBusy ? "not-allowed" : "pointer",
                  opacity: creatingRootBusy ? 0.6 : 1,
                }}
              >
                {t("fileTree.create")}
              </button>
              <button
                type="button"
                disabled={creatingRootBusy}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onCreateRootCancel?.()}
                style={{
                  border: "none",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  borderRadius: "6px",
                  padding: "4px 6px",
                  fontSize: "11px",
                  cursor: creatingRootBusy ? "not-allowed" : "pointer",
                  opacity: creatingRootBusy ? 0.6 : 1,
                }}
              >
                {t("common.cancel")}
              </button>
            </div>
          )}
        </div>
      </li>
    ) : null}
    {sortDirectoryEntries(visibleEntries(items, depth), sortMode).map((entry: FileEntry) => {
      const isManagedRootNode = entry.is_root === true;
      const entryRoot = isManagedRootNode ? entry.path : branchRoot;
      // 多节点：条目节点取所属分组注入的 sectionNodeId（root 条目带回 _nodeId 时优先自身）
      const entryNodeId = String(
        (entry as any)?._nodeId || sectionNodeId || "",
      ).trim();
      const expandedKey = isManagedRootNode
        ? scopeKey(entryNodeId, entry.path)
        : treeKey(entryNodeId, entryRoot, entry.path);
      const isOpen = expandedSet.has(expandedKey);

      const cKey = childKeyFor(entry, entryRoot, entryNodeId);
      const children = childrenByPath[cKey] ?? [];

      const isCurrentRootNode =
        isManagedRootNode &&
        entry.path === rootId &&
        String((entry as any)._nodeId || "") === String(rootNodeId || "");
      const isSelectedNode =
        String((entry as any)._nodeId || "") === String(rootNodeId || "");
      // 普通目录沿用 selectedDirKey；当前 managed root 永远跟随 current root 高亮。
      // 多节点同名项目：selectedDirKey 为裸 rootId，须叠加节点匹配，避免两侧同时高亮。
      const isSelected =
        entry.is_dir
          ? isCurrentRootNode || (isSelectedNode && selectedDirKey === expandedKey)
          : entry.path === selectedPath &&
            entryRoot === rootId &&
            String(entryNodeId) === String(rootNodeId || "");

      // 选中态中性灰条 + 节点色文字：文件/目录选中时文字用所属项目节点色
      const selectedNodeColor = String((entry as any)._nodeColor || "").trim();
      const effectiveSelectedColor = isSelected ? (selectedNodeColor || String(groupColor || "").trim() || "") : "";

      const meta = fileMetas[entry.path];
      const hasSessionLink = !entry.is_dir && meta?.source_session;
      const isFromActiveSession = hasSessionLink && meta.source_session === activeSessionKey;
      const rootIndicator = isManagedRootNode
        ? rootSessionIndicators[scopeKey(entryNodeId, entry.path)] || {}
        : null;
      const showRootIndicator = !!rootIndicator?.bound;
      const isRootPending = !!rootIndicator?.pending;
      const handleEntryClick = () => {
        if (entry.is_dir) {
          if (isManagedRootNode) {
            (onSelectRoot || onToggleDir)?.(entry, entryRoot);
            return;
          }
          // 点目录「名字」= 打开该目录（chevron 才是纯展开收起）
          (onSelectDir || onToggleDir)?.(entry, entryRoot);
          return;
        }
        onSelectFile?.(entry, entryRoot);
      };
      const handleDirectoryIconClick = (event: React.MouseEvent<HTMLSpanElement>) => {
        if (!entry.is_dir) {
          return;
        }
        event.stopPropagation();
        onToggleDir?.(entry, entryRoot);
      };

      const rootExtraContent = isManagedRootNode
        ? projectTreeTab === "git"
          ? renderRootExtraContent?.(entry.path)
          : projectTreeTab === "worktrees"
            ? renderRootWorktreeContent?.(entry.path)
          : projectTreeTab === "related"
            ? renderRootRelatedContent?.(entry.path)
            : null
        : null;
      const shouldRenderChildren = projectTreeTab === "files" || !isManagedRootNode;

      return (
        <li key={expandedKey}>
          <button
            type="button"
            onClick={handleEntryClick}
            style={{
              border: "none",
              background: isSelected ? "var(--node-row-selected-bg)" : "transparent",
              cursor: "pointer",
              padding: "6px 8px",
              paddingLeft: PROJECT_TREE_ROOT_PADDING_LEFT + depth * PROJECT_TREE_INDENT,
              display: "flex",
              alignItems: "center",
              gap: "4px",
              width: "100%",
              textAlign: "left",
              color: isSelected ? (effectiveSelectedColor || "var(--accent-color)") : "var(--text-primary)",
              fontSize: "13px",
              borderRadius: "6px",
              transition: "all 0.1s",
              fontWeight: isManagedRootNode ? 600 : 400,
              outline: "none",
            }}
            onMouseEnter={(e) => { if (!isSelected) e.currentTarget.style.background = "rgba(0,0,0,0.04)"; }}
            onMouseLeave={(e) => { if (!isSelected) e.currentTarget.style.background = "transparent"; }}
          >
            <span
              onClick={handleDirectoryIconClick}
              title={entry.is_dir ? (isOpen ? t("common.collapse") : t("common.expand")) : undefined}
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                borderRadius: "4px",
                cursor: entry.is_dir ? "pointer" : "default",
              }}
            >
              <DirectoryIconSlot entry={entry} isOpen={isOpen} />
            </span>
            <span
              style={{
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
                flex: 1,
                marginLeft: "4px",
              }}
            >
              {(() => {
                const exAny = entry as any;
                const nodeColor = String(exAny._nodeColor || "#6d5bcf").trim() || "#6d5bcf";
                const styleForRoot = isManagedRootNode
                  ? { ...rootBadgeStyle, background: "var(--node-badge-bg)", color: nodeColor, fontWeight: 600 as const }
                  : {};
                return (
                  <span
                    style={{
                      ...styleForRoot,
                      maxWidth: "100%",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      fontWeight: isManagedRootNode ? 600 : 400,
                    }}
                  >
                    {entry.name}
                  </span>
                );
              })()}
            </span>
            {showRootIndicator ? (
              <span
                aria-label={isRootPending ? t("fileTree.boundSessionReplying") : t("fileTree.boundSession")}
                title={isRootPending ? t("fileTree.boundSessionReplying") : t("fileTree.boundSession")}
                style={{
                  width: "8px",
                  height: "8px",
                  borderRadius: "999px",
                  flexShrink: 0,
                  boxSizing: "border-box",
                  border: `1.5px solid ${String(groupColor || (entry as any)?._nodeColor || "").trim() || DEFAULT_NODE_COLOR}`,
                  background: isRootPending ? (String(groupColor || (entry as any)?._nodeColor || "").trim() || DEFAULT_NODE_COLOR) : "transparent",
                  animation: isRootPending ? "mindfs-bound-pulse 2.2s ease-in-out infinite" : "none",
                  boxShadow: isRootPending
                    ? `0 0 0 1.5px ${hexToRgbaApp(String(groupColor || (entry as any)?._nodeColor || "").trim() || DEFAULT_NODE_COLOR, 0.14)}`
                    : `0 0 0 1px ${hexToRgbaApp(String(groupColor || (entry as any)?._nodeColor || "").trim() || DEFAULT_NODE_COLOR, 0.10)}`,
                }}
              />
            ) : null}
            {hasSessionLink && (
              <span style={{ fontSize: "10px", color: isFromActiveSession ? (String(groupColor || (entry as any)?._nodeColor || "").trim() || DEFAULT_NODE_COLOR) : "#9ca3af" }}>
                {isFromActiveSession ? "◆" : "◇"}
              </span>
            )}
          </button>
          {entry.is_dir && isOpen && shouldRenderChildren && children.length > 0 ? renderEntries(children, depth + 1, entryRoot, groupColor || (entry as any)._nodeColor || (isManagedRootNode ? String((entry as any)._nodeColor || "") : groupColor), entryNodeId) : null}
          {entry.is_dir && isOpen && rootExtraContent ? (
            <div style={{ padding: `2px 4px 8px ${PROJECT_TREE_INDENT}px` }}>
              {rootExtraContent}
            </div>
          ) : null}
        </li>
      );
    })}
  </ul>
);
  return renderEntries;
}
