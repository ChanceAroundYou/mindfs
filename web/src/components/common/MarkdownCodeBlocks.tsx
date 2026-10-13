import { memo, useEffect, useMemo, useRef, useState } from "react";
import Prism from "prismjs";
import { copyText } from "../../services/platform/clipboard";
import { useI18n } from "../../i18n/index";
import { buildDiffCodeRows, type DiffCodeRow } from "../../shared/gitDiffModel";
import { DiagramPreview } from "./DiagramPreview";
import "prismjs/themes/prism.css";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-json";
import "prismjs/components/prism-diff";

// 从 MarkdownViewer.tsx 拆出：mermaid 渲染、diff 行样式、代码块（含 Prism 高亮与复制按钮）。
// 这些块原本就自成一体的兄弟声明，只是寄居在 MarkdownViewer.tsx 里。
export const monoFontFamily = [
  '"SFMono-Regular"',
  '"Cascadia Mono"',
  '"Sarasa Mono SC"',
  '"Noto Sans Mono CJK SC"',
  '"Source Han Mono SC"',
  'Menlo',
  'Monaco',
  '"Courier New"',
  'monospace',
].join(", ");

let mermaidInitialized = false;
let mermaidRenderId = 0;
let mermaidModulePromise: Promise<typeof import("mermaid")> | null = null;

async function getMermaid() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import("mermaid");
  }
  const mod = await mermaidModulePromise;
  return mod.default;
}

async function ensureMermaidInitialized() {
  const mermaid = await getMermaid();
  if (mermaidInitialized) return;
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: "default",
    suppressErrorRendering: true,
  });
  mermaidInitialized = true;
}

/**
 * mermaid 11.x 无法解析 flowchart 节点标签里的裸竖线（`B[|μ|]` 会报
 * "Syntax error in text"，`|` 被当成边标签分隔符）。这里仅对 flowchart/graph
 * 中未加引号的 `[]` 标签做引号包裹；已引号/含引号/其他图类型的输入原样保留。
 */
function makeMermaidSafe(source: string): string {
  if (!/^\s*(flowchart|graph)\b/m.test(source)) return source;
  return source.replace(
    /\b([A-Za-z_][\w-]*)\[([^\[\]"]*\|[^\[\]"]*)\]/g,
    (_, id, label) => `${id}["${label}"]`,
  );
}

export function MermaidBlock({ chart }: { chart: string }) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const blockRef = useRef<HTMLDivElement>(null);
  const [previewHeight, setPreviewHeight] = useState(0);
  const [svg, setSvg] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;

    const renderChart = async () => {
      const source = makeMermaidSafe(chart.trim());
      if (!source) {
        setSvg("");
        setError("");
        return;
      }

      await ensureMermaidInitialized();
      const mermaid = await getMermaid();
      const renderId = `mindfs-mermaid-${mermaidRenderId += 1}`;

      try {
        const { svg: renderedSvg } = await mermaid.render(renderId, source);
        if (!cancelled) {
          setSvg(renderedSvg);
          setError("");
        }
      } catch (err) {
        if (!cancelled) {
          setSvg("");
          setError(err instanceof Error ? err.message : "Failed to render Mermaid diagram.");
        }
      }
    };

    void renderChart();

    return () => {
      cancelled = true;
    };
  }, [chart]);

  if (error) {
    return (
      <pre
        style={{
          width: "100%",
          boxSizing: "border-box",
          background: "rgba(127, 29, 29, 0.05)",
          color: "#991b1b",
          padding: "16px",
          borderRadius: "10px",
          overflow: "auto",
          border: "1px solid rgba(239, 68, 68, 0.25)",
          fontFamily: monoFontFamily,
          fontSize: "13px",
          margin: "1.5em 0",
          lineHeight: "1.6",
          whiteSpace: "pre-wrap",
        }}
      >
        {`Mermaid render error\n\n${error}\n\n${chart}`}
      </pre>
    );
  }

  return (
    <div
      ref={blockRef}
      style={{
        position: "relative",
        width: "100%",
        minHeight: expanded ? previewHeight : undefined,
        boxSizing: "border-box",
        background: "rgba(0,0,0,0.02)",
        padding: "16px",
        borderRadius: "10px",
        overflow: "auto",
        border: "1px solid var(--border-color)",
        margin: "1.5em 0",
      }}
    >
      {svg ? (
        <>
          <button type="button" className="diagram-expand" aria-label={t("diagram.expand")} title={t("diagram.expand")} onClick={() => {
            setPreviewHeight(blockRef.current?.getBoundingClientRect().height || 0);
            setExpanded(true);
          }}>
            <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M8 3H3v5M16 3h5v5M21 16v5h-5M3 16v5h5" />
              <path d="m3 3 6 6m12-6-6 6m6 12-6-6M3 21l6-6" />
            </svg>
          </button>
          {expanded ? <DiagramPreview svg={svg} onClose={() => setExpanded(false)} /> : (
            <div
              dangerouslySetInnerHTML={{ __html: svg }}
              style={{ minWidth: "fit-content" }}
            />
          )}
        </>
      ) : (
        <div style={{ color: "var(--text-secondary)", fontSize: "14px" }}>Rendering Mermaid diagram...</div>
      )}
    </div>
  );
}

function diffCodeRowPrefix(kind: DiffCodeRow["kind"]): string {
  if (kind === "add") return "+";
  if (kind === "del") return "-";
  if (kind === "ctx") return " ";
  return "";
}

function diffCodeRowStyle(kind: DiffCodeRow["kind"]): { background: string; color: string } {
  if (kind === "add") {
    return { background: "rgba(34, 197, 94, 0.14)", color: "#166534" };
  }
  if (kind === "del") {
    return { background: "rgba(239, 68, 68, 0.14)", color: "#991b1b" };
  }
  if (kind === "hunk") {
    return { background: "color-mix(in srgb, var(--accent-color) 10%, transparent)", color: "var(--accent-color)" };
  }
  if (kind === "meta") {
    return { background: "rgba(100, 116, 139, 0.10)", color: "#475569" };
  }
  return { background: "transparent", color: "inherit" };
}

function diffCodeSegmentBackground(rowKind: DiffCodeRow["kind"], segmentKind: "ctx" | "add" | "del"): string {
  if (rowKind === "add" && segmentKind === "add") {
    return "rgba(22, 163, 74, 0.22)";
  }
  if (rowKind === "del" && segmentKind === "del") {
    return "rgba(220, 38, 38, 0.22)";
  }
  return "transparent";
}

function renderDiffCode(rawContent: string) {
  const rows = buildDiffCodeRows(rawContent);
  return rows.map((row, index) => {
    const { background, color } = diffCodeRowStyle(row.kind);
    const prefix = diffCodeRowPrefix(row.kind);
    const content = row.segments?.length
      ? row.segments.map((segment, segmentIndex) => (
        <span
          key={`${segmentIndex}-${segment.kind}-${segment.text}`}
          style={{
            background: diffCodeSegmentBackground(row.kind, segment.kind),
            borderRadius: segment.kind === "ctx" ? 0 : "3px",
          }}
        >
          {segment.text}
        </span>
      ))
      : row.text || " ";

    return (
      <span
        key={`${index}-${row.kind}-${row.text}`}
        style={{
          display: "block",
          margin: "0 -8px",
          padding: "0 8px",
          background,
          color,
        }}
      >
        {prefix}{content}
      </span>
    );
  });
}

export function MarkdownCodeBlock({
  className,
  rawContent,
  language,
  sourceLineProps,
}: {
  className: string;
  rawContent: string;
  language: string;
  sourceLineProps: Record<string, unknown>;
}) {
  const { t } = useI18n();
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const resetTimerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (resetTimerRef.current) {
        window.clearTimeout(resetTimerRef.current);
      }
    };
  }, []);

  const highlightedHtml = useMemo(() => {
    if (!language || language === "diff") return "";
    const grammar = Prism.languages[language] ?? Prism.languages.markup;
    try {
      return Prism.highlight(rawContent, grammar, language);
    } catch {
      return "";
    }
  }, [language, rawContent]);

  const handleCopy = () => {
    if (resetTimerRef.current) {
      window.clearTimeout(resetTimerRef.current);
    }
    void copyText(rawContent)
      .then(() => {
        setCopyState("copied");
      })
      .catch(() => {
        setCopyState("failed");
      })
      .finally(() => {
        resetTimerRef.current = window.setTimeout(() => {
          setCopyState("idle");
          resetTimerRef.current = null;
        }, 1200);
      });
  };

  const isCopied = copyState === "copied";
  const isFailed = copyState === "failed";

  return (
    <div
      {...sourceLineProps}
      style={{
        position: "relative",
        width: "100%",
        boxSizing: "border-box",
        margin: "1.5em 0",
      }}
    >
      <button
        type="button"
        onClick={handleCopy}
        aria-label={isCopied ? t("markdown.codeCopied") : t("markdown.copyCode")}
        title={isCopied ? t("markdown.copied") : isFailed ? t("markdown.copyFailed") : t("markdown.copyCode")}
        style={{
          position: "absolute",
          top: "4px",
          right: "4px",
          zIndex: 1,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: "24px",
          height: "24px",
          borderRadius: "6px",
          border: "none",
          background: "transparent",
          color: isFailed ? "#b91c1c" : "var(--accent-color)",
          cursor: "pointer",
          opacity: isFailed ? 1 : 0.5,
          padding: 0,
        }}
      >
        {isCopied ? (
          <span
            aria-hidden="true"
            style={{
              fontSize: "13px",
              fontWeight: 800,
              lineHeight: 1,
            }}
          >
            ✓
          </span>
        ) : (
          <svg
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden="true"
            width="14"
            height="14"
            viewBox="0 0 24 24"
          >
            <path
              fill="currentColor"
              d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
            />
            <path
              fill="currentColor"
              d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
            />
          </svg>
        )}
      </button>
      <pre
        className={className}
        style={{
          width: "100%",
          boxSizing: "border-box",
          background: "var(--mindfs-code-bg, #f8fafc)",
          color: "var(--mindfs-code-text, var(--text-primary))",
          padding: "16px",
          borderRadius: "10px",
          overflow: "auto",
          border: "1px solid var(--mindfs-code-border, var(--border-color))",
          fontFamily: monoFontFamily,
          fontSize: "13px",
          margin: 0,
          lineHeight: "1.6",
          whiteSpace: "pre",
          tabSize: 2 as any,
          fontVariantLigatures: "none",
          boxShadow: "none",
        }}
      >
        {language === "diff" ? (
          <code
            className={className}
            style={{
              display: "block",
              textShadow: "none",
              fontFamily: monoFontFamily,
              tabSize: 2 as any,
              fontVariantLigatures: "none",
              whiteSpace: "pre",
              border: "none",
              background: "transparent",
            }}
          >
            {renderDiffCode(rawContent)}
          </code>
        ) : highlightedHtml ? (
          <code
            className={className}
            dangerouslySetInnerHTML={{ __html: highlightedHtml }}
            style={{ display: "block", textShadow: "none", fontFamily: monoFontFamily, border: "none", background: "transparent" }}
          />
        ) : (
          <code
            className={className}
            style={{
              display: "block",
              textShadow: "none",
              fontFamily: monoFontFamily,
              tabSize: 2 as any,
              fontVariantLigatures: "none",
              whiteSpace: "pre",
              border: "none",
              background: "transparent",
            }}
          >
            {rawContent}
          </code>
        )}
      </pre>
    </div>
  );
}

// memo 化代码块：rawContent/language 不变时跳过整块重渲染（含 Prism.highlight 与复制按钮状态）。
// 忽略 sourceLineProps（data-source-line 属性对象每次新建，不影响内容）。
export const MarkdownCodeBlockMemo = memo(MarkdownCodeBlock, (prev, next) =>
  prev.className === next.className &&
  prev.rawContent === next.rawContent &&
  prev.language === next.language,
);
