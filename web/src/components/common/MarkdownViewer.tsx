import React, { memo } from "react";
import { useI18n } from "../../i18n/index";
import { useMarkdownViewer } from "../../hooks/useMarkdownViewer";

function MarkdownViewerInner({
  content,
  currentPath = "",
  root,
  onFileClick,
  targetLine,
  contentRef,
  scrollContainerRef,
  isVisible = true,
  compactOutline = false,
  showOutline = false,
}: {
  content: string;
  currentPath?: string;
  root?: string;
  onFileClick?: (path: string) => void;
  targetLine?: number;
  contentRef?: React.RefObject<HTMLDivElement | null>;
  scrollContainerRef?: React.RefObject<HTMLDivElement | null>;
  isVisible?: boolean;
  compactOutline?: boolean;
  showOutline?: boolean;
}) {
  const { t } = useI18n();
  const {
    viewer,
    outline,
    activeHeadingId,
    isOutlineCollapsed,
    setIsOutlineCollapsed,
    setActiveHeadingId,
    forcedActiveHeadingRef,
  } = useMarkdownViewer({
    content,
    currentPath,
    root,
    onFileClick,
    targetLine,
    contentRef,
    scrollContainerRef,
    isVisible,
    compactOutline,
    showOutline,
  });

  if (!showOutline || outline.length === 0) return viewer;

  return (
    <div className={`markdown-document-layout${isOutlineCollapsed ? " is-outline-collapsed" : ""}${compactOutline ? " is-mobile-outline" : ""}`}>
      {compactOutline && !isOutlineCollapsed && (
        <button
          type="button"
          className="markdown-outline-backdrop"
          aria-label={t("markdown.collapseOutline")}
          onClick={() => setIsOutlineCollapsed(true)}
        />
      )}
      <aside className={`markdown-outline${isOutlineCollapsed ? " is-collapsed" : ""}`} aria-label={t("markdown.outline")}>
        <button
          type="button"
          className="markdown-outline-toggle"
          aria-label={t(isOutlineCollapsed ? "markdown.expandOutline" : "markdown.collapseOutline")}
          title={t(isOutlineCollapsed ? "markdown.expandOutline" : "markdown.collapseOutline")}
          aria-expanded={!isOutlineCollapsed}
          onClick={() => setIsOutlineCollapsed((collapsed) => !collapsed)}
        >
          {isOutlineCollapsed && (
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m10 3-5 5 5 5" /></svg>
          )}
          <span>{t("markdown.outline")}</span>
          {!isOutlineCollapsed && (
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 3 5 5-5 5" /></svg>
          )}
        </button>
        <nav>
          {outline.map((item) => (
            <button
              type="button"
              key={item.id}
              className={`markdown-outline-item${activeHeadingId === item.id ? " is-active" : ""}`}
              style={{ "--outline-level": item.level } as React.CSSProperties}
              title={item.title}
              onClick={() => {
                const heading = document.getElementById(item.id);
                const scrollContainer = scrollContainerRef?.current;
                forcedActiveHeadingRef.current = item.id;
                if (heading && scrollContainer) {
                  const nextScrollTop = scrollContainer.scrollTop
                    + heading.getBoundingClientRect().top
                    - scrollContainer.getBoundingClientRect().top
                    - 24;
                  scrollContainer.scrollTop = Math.max(0, nextScrollTop);
                } else {
                  heading?.scrollIntoView({ behavior: "auto", block: "start" });
                }
                setActiveHeadingId(item.id);
                if (compactOutline) setIsOutlineCollapsed(true);
              }}
            >
              {item.title}
            </button>
          ))}
        </nav>
      </aside>
      {viewer}
    </div>
  );
}

export const MarkdownViewer = memo(MarkdownViewerInner, (prev, next) => (
  prev.content === next.content &&
  prev.currentPath === next.currentPath &&
  prev.root === next.root &&
  prev.targetLine === next.targetLine &&
  prev.isVisible === next.isVisible &&
  prev.compactOutline === next.compactOutline &&
  prev.showOutline === next.showOutline
));
