import React, { useEffect, useRef, useState } from "react";
import { MarkdownViewer } from "./MarkdownViewer";
import { CodeViewer } from "./CodeViewer";
import { ImageViewer } from "./ImageViewer";
import { BinaryViewer } from "./BinaryViewer";
import { DocumentViewer } from "./DocumentViewer";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { useI18n } from "../../i18n/index";
import { ViewHeader } from "../../layout/ViewHeader";
import { FileEditor } from "./FileEditor";
import { FileOperationItems, FileMenuIcon, fileMenuItemStyle, MoreMenuButton, moreMenuPopoverStyle, menuOverlayStyle, MoveFilePopover } from "./FileOperations";
import { useFileViewer, type FileViewerProps } from "./useFileViewer";

function FileViewerMenu({ root, rootPath, path, downloading, onDownload, onEdit, onComplete }: {
  root: string; rootPath?: string; path: string; downloading: boolean; onDownload: () => void;
  onEdit?: () => void; onComplete?: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open && !moveOpen) return;
    const close = (event: PointerEvent) => { if (!ref.current?.contains(event.target as Node)) { setOpen(false); setMoveOpen(false); } };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open, moveOpen]);
  return <div ref={ref} style={{ position: "relative" }} onKeyDown={event => { if (event.key === "Escape") { setOpen(false); setMoveOpen(false); } }}>
    <MoreMenuButton open={open} label={t("fileOperation.menu")} onClick={() => { setMoveOpen(false); setOpen(value => !value); }} />
    {open && <div role="menu" style={moreMenuPopoverStyle}>
      <button type="button" role="menuitem" style={fileMenuItemStyle} disabled={!root || downloading} onClick={() => { setOpen(false); onDownload(); }}><FileMenuIcon action="download" /><span>{t("fileOperation.download")}</span></button>
      {onEdit && <button type="button" role="menuitem" style={fileMenuItemStyle} onClick={() => { setOpen(false); onEdit(); }}><FileMenuIcon action="edit" /><span>{t("fileEditor.edit")}</span></button>}
      {root && !/^(?:\/|[A-Za-z]:)/.test(path) && <FileOperationItems root={root} path={path} onMove={() => { setOpen(false); setMoveOpen(true); }} onComplete={async () => { await onComplete?.(); setOpen(false); }} />}
    </div>}
    {moveOpen && <div style={menuOverlayStyle}><MoveFilePopover root={root} rootPath={rootPath} path={path} onComplete={async () => { setMoveOpen(false); await onComplete?.(); }} /></div>}
  </div>;
}

function Breadcrumbs({ root, rootDisplayName, path, rootColor, onPathClick }: { root?: string; rootDisplayName?: string | null; path: string; rootColor?: string | null; onPathClick?: (path: string) => void }) {
  const parts = path.split('/').filter(Boolean);
  const getPathAt = (index: number) => parts.slice(0, index + 1).join('/');

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '13px', color: 'var(--text-secondary)', overflow: 'hidden', whiteSpace: 'nowrap', flexShrink: 1, justifyContent: 'flex-start' }}>
      {root && (
        <>
          <button
            type="button"
            data-onboarding="project-home"
            onClick={() => onPathClick?.(".")}
            style={{
              ...rootBadgeButtonStyle,
              background: "var(--node-badge-bg)",
              color: String(rootColor || "").trim() || "var(--text-primary)",
              cursor: "pointer",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
            onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}
          >
            {rootDisplayName || root}
          </button>
          {parts.length > 0 && <span style={{ opacity: 0.4, fontSize: '10px', flexShrink: 0 }}>❯</span>}
        </>
      )}
      {parts.map((part, index) => (
        <React.Fragment key={index}>
          <span
            onClick={() => index < parts.length - 1 && onPathClick?.(getPathAt(index))}
            style={{ fontWeight: index === parts.length - 1 ? 600 : 400, color: index === parts.length - 1 ? 'var(--text-primary)' : 'inherit', cursor: index < parts.length - 1 ? 'pointer' : 'default', overflow: 'hidden', textOverflow: 'ellipsis' }}
            onMouseEnter={(e) => { if (index < parts.length - 1) e.currentTarget.style.textDecoration = 'underline'; }}
            onMouseLeave={(e) => { e.currentTarget.style.textDecoration = 'none'; }}
          >
            {part}
          </span>
          {index < parts.length - 1 && <span style={{ opacity: 0.4, fontSize: '10px', flexShrink: 0 }}>❯</span>}
        </React.Fragment>
      ))}
    </div>
  );
}

export function FileViewer(props: FileViewerProps) {
  const {
    editKey,
    editSession,
    canEdit,
    isDownloading,
    editorActions,
    setEditorActions,
    scrollRef,
    contentRootRef,
    codeContentRef,
    isMobile,
    fileScrollKey,
    ext,
    documentPreviewKind,
    usesMarkdownViewer,
    downloadToast,
    showToast,
    handleDownload,
    relatedSessions,
    visibleRelatedSessions,
  } = useFileViewer(props);

  const { file, rootPath, rootDisplayName, rootColor, onSessionClick, onPathClick, onFileClick, onFileOperationComplete, onFileUpdated, onFileSaved, editStore, isVisible = true } = props;

  if (!file) {
    return (
      <div style={{ flex: 1, minHeight: 0, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-secondary)", flexDirection: "column", gap: "12px" }}>
        <div style={{ fontSize: "48px", opacity: 0.2 }}>📄</div>
        <p>Select a file to preview</p>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0, minWidth: 0, background: "transparent" }}>
      {/* 下载结果 toast */}
      {downloadToast && (
        <div style={{
          position: "fixed",
          bottom: "24px",
          left: "50%",
          transform: "translateX(-50%)",
          background: downloadToast.ok ? "rgba(34,197,94,0.92)" : "rgba(239,68,68,0.92)",
          color: "#fff",
          padding: "10px 20px",
          borderRadius: "8px",
          fontSize: "14px",
          fontWeight: 500,
          zIndex: 9999,
          pointerEvents: "none",
          boxShadow: "0 4px 12px rgba(0,0,0,0.2)",
          whiteSpace: "nowrap",
        }}>
          {downloadToast.msg}
        </div>
      )}
      <ViewHeader padding="0 3px 0 16px" style={{ gap: "10px", zIndex: 10 }} innerStyle={{ gap: "10px" }}>
        <div style={{ display: "flex", alignItems: "center", overflow: "hidden", flex: 1, minWidth: 0 }}>
          <Breadcrumbs root={file.root} rootDisplayName={rootDisplayName} path={file.path} rootColor={rootColor} onPathClick={onPathClick} />

          {relatedSessions.length > 0 && (
            <div style={{
              marginLeft: "16px",
              display: "flex",
              alignItems: "center",
              gap: "6px",
              minWidth: 0,
              flexShrink: 0
            }}>
              {/* 替换文字为图标 */}
              <svg
                width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                style={{ color: "var(--text-secondary)", opacity: 0.4 }}
              >
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
              </svg>
              <div style={{ display: 'flex', gap: '4px', overflowX: 'auto', whiteSpace: 'nowrap', scrollbarWidth: 'none' }}>
                {visibleRelatedSessions.map((meta) => (
                  <button
                    key={meta.source_session}
                    type="button"
                    onClick={(e) => {
                      e.preventDefault(); e.stopPropagation();
                      if (meta.source_session) {
                        onSessionClick?.(meta.source_session);
                      }
                    }}
                    style={{
                      background: "rgba(0, 0, 0, 0.03)",
                      border: "1px solid rgba(0, 0, 0, 0.05)",
                      borderRadius: "6px",
                      padding: "1px 8px",
                      cursor: "pointer",
                      color: "var(--text-secondary)",
                      fontSize: "11px",
                      fontWeight: 500,
                      flexShrink: 0,
                      transition: "all 0.2s ease"
                    }}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.background = "var(--selection-bg)";
                      e.currentTarget.style.color = "var(--accent-color)";
                      e.currentTarget.style.borderColor = "color-mix(in srgb, var(--accent-color) 20%, transparent)";
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.background = "rgba(0, 0, 0, 0.03)";
                      e.currentTarget.style.color = "var(--text-secondary)";
                      e.currentTarget.style.borderColor = "rgba(0, 0, 0, 0.05)";
                    }}
                  >
                    <span
                      style={{
                        display: "inline-block",
                        maxWidth: isMobile ? "72px" : "120px",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                        verticalAlign: "bottom",
                      }}
                      title={meta.session_name || `Session ${meta.source_session.slice(0, 8)}`}
                    >
                      {meta.session_name || `Session ${meta.source_session.slice(0, 8)}`}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
        <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: "2px", minWidth: 0, flexShrink: 0 }}>
          <div style={{ fontSize: "11px", color: "var(--text-secondary)", marginLeft: "6px", flexShrink: 0, opacity: 0.7 }}>{(file.size / 1024).toFixed(1)} KB</div>
          <span className="file-editor-actions" ref={setEditorActions} />
          {!editSession && <FileViewerMenu key={editKey} root={file.root || ""} rootPath={rootPath} path={file.path} downloading={isDownloading} onDownload={() => { void handleDownload(); }} onEdit={canEdit ? () => { void editStore.open(file.root!, file.path); } : undefined} onComplete={onFileOperationComplete} />}
        </div>
      </ViewHeader>

      {editSession ? <FileEditor key={editKey} actionsTarget={editorActions} store={editStore} editKey={editKey} session={editSession} isVisible={isVisible} onFileUpdated={onFileUpdated} onFileSaved={onFileSaved} onExitError={(error) => showToast(error, false)} /> : <div ref={scrollRef} style={{ flex: 1, minHeight: 0, overflow: documentPreviewKind === "powerpoint" ? "hidden" : "auto", position: "relative", WebkitOverflowScrolling: "touch" }}>
        <div style={{ minWidth: "100%", height: documentPreviewKind === "powerpoint" ? "100%" : undefined, display: "block", background: "transparent" }}>
          {documentPreviewKind ? (
            <DocumentViewer key={`${file.root || ""}:${file.path}`} path={file.path} root={file.root} kind={documentPreviewKind} />
          ) : file.mime?.startsWith("image/") || [".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"].includes(ext.toLowerCase()) ? (
            <div style={{ padding: "24px 16px" }}><ImageViewer path={file.path} root={file.root} /></div>
          ) : file.encoding === "binary" ? (
            <div style={{ padding: "24px 16px" }}><BinaryViewer /></div>
          ) : usesMarkdownViewer ? (
            <div ref={contentRootRef} className="markdown-document-shell" style={{ padding: "24px 16px" }}>
              <MarkdownViewer
                content={file.content}
                currentPath={file.path}
                root={file.root}
                onFileClick={onFileClick}
                targetLine={file.targetLine}
                contentRef={contentRootRef}
                scrollContainerRef={scrollRef}
                isVisible={isVisible}
                compactOutline={isMobile}
                showOutline
              />
            </div>
          ) : (
            <CodeViewer
              content={file.content}
              ext={ext}
              targetLine={file.targetLine}
              targetColumn={file.targetColumn}
              contentRef={codeContentRef}
            />
          )}
        </div>
      </div>}
    </div>
  );
}
