import React, { useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import rehypeRaw from "rehype-raw";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import Prism from "prismjs";
import { openExternalURL } from "../../services/platform/platformNavigation";
import { extractMarkdownOutline } from "../../shared/markdownOutline";
import { MarkdownCodeBlockMemo, MermaidBlock, monoFontFamily } from "./MarkdownCodeBlocks";
import {
  resolveMarkdownHref,
  isExternalHref,
  isDirectImageSrc,
  normalizeMarkdownMathDelimiters,
} from "./markdownUtils";
import { fetchProofProtectedBlob } from "../../services/file";
import "prismjs/themes/prism.css";
import "katex/dist/katex.min.css";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-json";
import "prismjs/components/prism-diff";


const markdownSanitizeSchema = {
  ...defaultSchema,
  attributes: {
    ...defaultSchema.attributes,
    p: [...(defaultSchema.attributes?.p || []), "align"],
    img: [...(defaultSchema.attributes?.img || []), "alt", "title", "width"],
  },
};

const EMPTY_OUTLINE: ReturnType<typeof extractMarkdownOutline> = [];

function MarkdownImage({
  src = "",
  alt = "",
  title,
  width,
  currentPath = "",
  root,
}: {
  src?: string;
  alt?: string;
  title?: string;
  width?: number | string;
  currentPath?: string;
  root?: string;
}) {
  const [resolvedSrc, setResolvedSrc] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";

    async function loadImage() {
      const trimmedSrc = src.trim();
      if (!trimmedSrc) {
        setResolvedSrc("");
        return;
      }

      if (isDirectImageSrc(trimmedSrc)) {
        setResolvedSrc(trimmedSrc);
        return;
      }

      if (!root) {
        setResolvedSrc(trimmedSrc);
        return;
      }

      try {
        const resolvedPath = resolveMarkdownHref(currentPath, trimmedSrc);
        if (!resolvedPath) {
          setResolvedSrc("");
          return;
        }
        const blob = await fetchProofProtectedBlob({ rootId: root, path: resolvedPath });
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setResolvedSrc(objectURL);
      } catch {
        if (!cancelled) {
          setResolvedSrc("");
        }
      }
    }

    void loadImage();

    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [currentPath, root, src]);

  if (!resolvedSrc) {
    return (
      <span
        style={{
          display: "inline-block",
          color: "var(--text-secondary)",
          fontSize: "13px",
        }}
      >
        {alt || title || src}
      </span>
    );
  }

  return <img src={resolvedSrc} alt={alt} title={title} width={width} />;
}

type UseMarkdownViewerOptions = {
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
};

export function useMarkdownViewer({
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
}: UseMarkdownViewerOptions) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const onFileClickRef = useRef(onFileClick);
  const sourceLineSelector = useMemo(() => {
    if (!targetLine || targetLine < 1) return "";
    return "[data-source-line]";
  }, [targetLine]);
  // 流式 chunk 高频更新时让 markdown 解析管线走低优先级（React 19 并发特性），
  // 保证输入/滚动等交互不被阻塞；内容静止后自动补渲染最新帧。
  const deferredContent = useDeferredValue(content);
  const normalizedContent = useMemo(
    () => normalizeMarkdownMathDelimiters(deferredContent),
    [deferredContent],
  );
  const outline = useMemo(
    () => (showOutline ? extractMarkdownOutline(normalizedContent) : EMPTY_OUTLINE),
    [normalizedContent, showOutline],
  );
  const headingsByLine = useMemo(
    () => new Map(outline.map((item) => [item.sourceLine, item])),
    [outline],
  );
  const [activeHeadingId, setActiveHeadingId] = useState("");
  const [isOutlineCollapsed, setIsOutlineCollapsed] = useState(compactOutline);
  const forcedActiveHeadingRef = useRef<string | null>(null);

  useEffect(() => {
    onFileClickRef.current = onFileClick;
  }, [onFileClick]);

  useEffect(() => {
    if (compactOutline) setIsOutlineCollapsed(true);
  }, [compactOutline]);

  useEffect(() => {
    if (!compactOutline || isOutlineCollapsed) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setIsOutlineCollapsed(true);
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [compactOutline, isOutlineCollapsed]);

  useEffect(() => {
    if (contentRef) {
      contentRef.current = containerRef.current;
    }
  }, [contentRef, content]);

  useEffect(() => {
    if (!targetLine || targetLine < 1 || !containerRef.current || !sourceLineSelector) {
      return;
    }
    const elements = Array.from(containerRef.current.querySelectorAll<HTMLElement>(sourceLineSelector));
    if (elements.length === 0) return;
    let target: HTMLElement | null = null;
    for (const el of elements) {
      const line = Number.parseInt(el.dataset.sourceLine || "", 10);
      if (!Number.isFinite(line)) continue;
      if (line <= targetLine) {
        target = el;
        continue;
      }
      break;
    }
    (target || elements[0]).scrollIntoView({ block: "center", behavior: "auto" });
  }, [content, sourceLineSelector, targetLine]);

  useEffect(() => {
    if (!isVisible || !showOutline || outline.length === 0 || !containerRef.current) {
      setActiveHeadingId("");
      return;
    }
    const container = containerRef.current;
    if (!container.querySelector("[data-markdown-heading]")) return;

    let scrollParent: HTMLElement | Window = scrollContainerRef?.current || window;
    if (!scrollContainerRef?.current) {
      let parent = container.parentElement;
      while (parent) {
        const overflowY = window.getComputedStyle(parent).overflowY;
        if (overflowY === "auto" || overflowY === "scroll") {
          scrollParent = parent;
          break;
        }
        parent = parent.parentElement;
      }
    }

    let frame = 0;
    const updateActiveHeading = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(() => {
        // ReactMarkdown may replace heading nodes after this effect is installed,
        // so always read the live DOM instead of retaining a stale NodeList.
        const headingElements = Array.from(container.querySelectorAll<HTMLElement>("[data-markdown-heading]"));
        if (headingElements.length === 0) return;
        const forcedActive = forcedActiveHeadingRef.current;
        if (forcedActive) {
          setActiveHeadingId(forcedActive);
          return;
        }
        const top = (scrollParent instanceof Window ? 24 : scrollParent.getBoundingClientRect().top + 24) + 2;
        let active = headingElements[0];
        for (const heading of headingElements) {
          if (heading.getBoundingClientRect().top <= top) active = heading;
          else break;
        }
        setActiveHeadingId(active.id);
      });
    };
    updateActiveHeading();
    const resizeObserver = typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver(updateActiveHeading);
    resizeObserver?.observe(container);
    const resumeScrollTracking = () => {
      forcedActiveHeadingRef.current = null;
    };
    scrollParent.addEventListener("wheel", resumeScrollTracking, { passive: true });
    scrollParent.addEventListener("touchstart", resumeScrollTracking, { passive: true });
    scrollParent.addEventListener("pointerdown", resumeScrollTracking, { passive: true });
    scrollParent.addEventListener("scroll", updateActiveHeading, { passive: true });
    window.addEventListener("resize", updateActiveHeading);
    return () => {
      window.cancelAnimationFrame(frame);
      resizeObserver?.disconnect();
      scrollParent.removeEventListener("wheel", resumeScrollTracking);
      scrollParent.removeEventListener("touchstart", resumeScrollTracking);
      scrollParent.removeEventListener("pointerdown", resumeScrollTracking);
      scrollParent.removeEventListener("scroll", updateActiveHeading);
      window.removeEventListener("resize", updateActiveHeading);
    };
  }, [isVisible, outline, scrollContainerRef, showOutline]);

  const getSourceLineProps = (node: any): Record<string, string> => {
    const line = node?.position?.start?.line;
    if (!Number.isFinite(line)) return {};
    return { "data-source-line": String(line) };
  };

  const getHeadingProps = (node: any): Record<string, string> => {
    const sourceProps = getSourceLineProps(node);
    const sourceLine = Number.parseInt(sourceProps["data-source-line"] || "", 10);
    const item = headingsByLine.get(sourceLine);
    return item
      ? { ...sourceProps, id: item.id, "data-markdown-heading": "true" }
      : sourceProps;
  };

  const hasFileClick = Boolean(onFileClick);
  // Component types must not change with each streamed chunk: that remounts
  // unchanged paragraphs, images, and code blocks instead of updating them.
  const markdownComponents = useMemo<NonNullable<React.ComponentProps<typeof ReactMarkdown>["components"]>>(() => ({
    h1: ({ node, ...props }: any) => (
      <h1 style={{ fontSize: "24px", marginTop: 0 }} {...getHeadingProps(node)} {...props} />
    ),
    h2: ({ node, ...props }: any) => (
      <h2 style={{ fontSize: "20px" }} {...getHeadingProps(node)} {...props} />
    ),
    h3: ({ node, ...props }: any) => (
      <h3 style={{ fontSize: "17px", marginTop: "1.25em" }} {...getHeadingProps(node)} {...props} />
    ),
    h4: ({ node, ...props }: any) => <h4 {...getHeadingProps(node)} {...props} />,
    h5: ({ node, ...props }: any) => <h5 {...getHeadingProps(node)} {...props} />,
    h6: ({ node, ...props }: any) => <h6 {...getHeadingProps(node)} {...props} />,
    p: ({ node, ...props }: any) => (
      <p style={{ margin: "0 0 1em", whiteSpace: "pre-wrap" }} {...getSourceLineProps(node)} {...props} />
    ),
    ul: ({ node, ...props }: any) => (
      <ul style={{ margin: "0 0 1em", paddingLeft: "1.4em" }} {...getSourceLineProps(node)} {...props} />
    ),
    ol: ({ node, ...props }: any) => (
      <ol style={{ margin: "0 0 1em", paddingLeft: "1.4em" }} {...getSourceLineProps(node)} {...props} />
    ),
    li: (props) => (
      <li style={{ margin: "0.2em 0" }} {...props} />
    ),
    img: ({ src, alt, title, width }) => (
      <MarkdownImage
        src={src}
        alt={alt}
        title={title}
        width={width}
        currentPath={currentPath}
        root={root}
      />
    ),
    a: ({ href = "", children, ...props }) => {
      if (!href || href.startsWith("#") || isExternalHref(href) || !hasFileClick) {
        const shouldOpenExternally = isExternalHref(href);
        return (
          <a
            {...props}
            href={href}
            style={{ color: "var(--accent-color)", cursor: shouldOpenExternally ? "pointer" : undefined }}
            onClick={
              shouldOpenExternally
                ? (event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    openExternalURL(href);
                  }
                : undefined
            }
          >
            {children}
          </a>
        );
      }
      const resolvedPath = resolveMarkdownHref(currentPath, href);
      return (
        <a
          href="#"
          onClick={(e) => {
            e.preventDefault();
            if (resolvedPath) {
              onFileClickRef.current?.(resolvedPath);
            }
          }}
          style={{ color: "var(--accent-color)", cursor: "pointer" }}
          {...props}
        >
          {children}
        </a>
      );
    },
    table: ({ node, ...props }: any) => (
      <div
        {...getSourceLineProps(node)}
        style={{
          width: "100%",
          overflowX: "auto",
          margin: "1.25em 0",
          border: "1px solid var(--border-color)",
          borderRadius: "10px",
          background: "rgba(0,0,0,0.02)",
        }}
      >
        <table
          style={{
            width: "100%",
            borderCollapse: "collapse",
            fontSize: "14px",
            lineHeight: 1.6,
            minWidth: "520px",
          }}
          {...props}
        />
      </div>
    ),
    thead: (props) => (
      <thead
        style={{
          background: "rgba(0,0,0,0.04)",
        }}
        {...props}
      />
    ),
    tr: (props) => (
      <tr
        style={{
          borderBottom: "1px solid var(--border-color)",
        }}
        {...props}
      />
    ),
    th: (props) => (
      <th
        style={{
          padding: "10px 12px",
          textAlign: "left",
          fontWeight: 600,
          whiteSpace: "nowrap",
          verticalAlign: "top",
        }}
        {...props}
      />
    ),
    td: (props) => (
      <td
        style={{
          padding: "10px 12px",
          verticalAlign: "top",
          borderTop: "1px solid rgba(0,0,0,0.03)",
        }}
        {...props}
      />
    ),
    blockquote: ({ node, ...props }: any) => (
      <blockquote style={{
        borderLeft: "3px solid var(--accent-color)",
        margin: "1.5em 0",
        paddingLeft: "16px",
        color: "var(--text-secondary)",
        fontStyle: "italic",
        background: "rgba(0,0,0,0.02)",
        padding: "12px 16px",
        borderRadius: "0 8px 8px 0"
      }} {...getSourceLineProps(node)} {...props} />
    ),
    code({ className, children, ...props }: any) {
      return (
        <code
          className={className}
          style={{
            background: "rgba(0,0,0,0.05)",
            padding: "2px 4px",
            borderRadius: "4px",
            color: "inherit",
            fontFamily: monoFontFamily,
            fontSize: "0.9em",
          }}
          {...props}
        >
          {children}
        </code>
      );
    },
    pre: ({ node, children }: any) => {
      const codeElement = React.Children.only(children) as React.ReactElement<any>;
      const className = codeElement?.props?.className || "";
      const rawContent = String(codeElement?.props?.children ?? "").replace(/\n$/, "");
      const match = /language-(\w+)/.exec(className);
      const language = match ? match[1] : "";

      if (language === "mermaid") {
        return (
          <div {...getSourceLineProps(node)}>
            <MermaidBlock chart={rawContent} />
          </div>
        );
      }

      return (
        <MarkdownCodeBlockMemo
          className={className}
          rawContent={rawContent}
          language={language}
          sourceLineProps={getSourceLineProps(node)}
        />
      );
    },
  }), [currentPath, headingsByLine, hasFileClick, root]);

  const renderedMarkdown = useMemo(() => (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath]}
      remarkRehypeOptions={{ allowDangerousHtml: true }}
      rehypePlugins={[rehypeRaw, [rehypeSanitize, markdownSanitizeSchema], rehypeKatex]}
      components={markdownComponents}
    >
      {normalizedContent}
    </ReactMarkdown>
  ), [markdownComponents, normalizedContent]);

  const viewer = (
    <div
      ref={containerRef}
      className="markdown-viewer"
      style={{
        padding: "0", // 移除内层 padding，由 FileViewer 统一控制
        color: "var(--text-primary)",
        lineHeight: 1.75,
        fontSize: "15px",
      }}
    >
      {renderedMarkdown}
    </div>
  );

  return {
    viewer,
    outline,
    activeHeadingId,
    isOutlineCollapsed,
    setIsOutlineCollapsed,
    setActiveHeadingId,
    forcedActiveHeadingRef,
  };
}
