import { useLayoutEffect, useMemo, type CSSProperties } from "react";

// 工具调用卡片的 ANSI 终端输出渲染（解析 / 样式 / 常量），
// 从 ToolCallCard 抽出；statusColors 与 terminalFontFamily 宿主仍在使用。

function normalizeTerminalText(text: string): string {
  return (text || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

type AnsiStyle = {
  color?: string;
  backgroundColor?: string;
  fontWeight?: CSSProperties["fontWeight"];
  fontStyle?: CSSProperties["fontStyle"];
  opacity?: number;
  textDecorationLine?: CSSProperties["textDecorationLine"];
};

type AnsiSegment = {
  text: string;
  style?: AnsiStyle;
};

const ansiBaseColors = [
  "#0f172a",
  "#ef4444",
  "#22c55e",
  "#eab308",
  "#3b82f6",
  "#a855f7",
  "#06b6d4",
  "#e5e7eb",
];

const ansiBrightColors = [
  "#475569",
  "#f87171",
  "#4ade80",
  "#facc15",
  "#60a5fa",
  "#c084fc",
  "#22d3ee",
  "#ffffff",
];

function ansi256Color(index: number): string | undefined {
  if (index >= 0 && index < 8) return ansiBaseColors[index];
  if (index >= 8 && index < 16) return ansiBrightColors[index - 8];
  if (index >= 16 && index <= 231) {
    const value = index - 16;
    const r = Math.floor(value / 36);
    const g = Math.floor((value % 36) / 6);
    const b = value % 6;
    const scale = (channel: number) => (channel === 0 ? 0 : 55 + channel * 40);
    return `rgb(${scale(r)}, ${scale(g)}, ${scale(b)})`;
  }
  if (index >= 232 && index <= 255) {
    const level = 8 + (index - 232) * 10;
    return `rgb(${level}, ${level}, ${level})`;
  }
  return undefined;
}

function cloneAnsiStyle(style: AnsiStyle): AnsiStyle | undefined {
  return Object.keys(style).length > 0 ? { ...style } : undefined;
}

function pushAnsiSegment(segments: AnsiSegment[], text: string, style: AnsiStyle) {
  if (!text) return;
  const currentStyle = cloneAnsiStyle(style);
  const previous = segments[segments.length - 1];
  if (previous && JSON.stringify(previous.style || {}) === JSON.stringify(currentStyle || {})) {
    previous.text += text;
    return;
  }
  segments.push({ text, style: currentStyle });
}

function applyAnsiCodes(style: AnsiStyle, rawCodes: string) {
  const codes = rawCodes === "" ? [0] : rawCodes.split(";").map((value) => Number.parseInt(value, 10)).map((value) => (Number.isFinite(value) ? value : 0));
  for (let i = 0; i < codes.length; i += 1) {
    const code = codes[i];
    if (code === 0) {
      delete style.color;
      delete style.backgroundColor;
      delete style.fontWeight;
      delete style.fontStyle;
      delete style.opacity;
      delete style.textDecorationLine;
    } else if (code === 1) {
      style.fontWeight = 700;
    } else if (code === 2) {
      style.opacity = 0.72;
    } else if (code === 3) {
      style.fontStyle = "italic";
    } else if (code === 4) {
      style.textDecorationLine = mergeTextDecoration(style.textDecorationLine, "underline");
    } else if (code === 9) {
      style.textDecorationLine = mergeTextDecoration(style.textDecorationLine, "line-through");
    } else if (code === 22) {
      delete style.fontWeight;
      delete style.opacity;
    } else if (code === 23) {
      delete style.fontStyle;
    } else if (code === 24) {
      style.textDecorationLine = removeTextDecoration(style.textDecorationLine, "underline");
    } else if (code === 29) {
      style.textDecorationLine = removeTextDecoration(style.textDecorationLine, "line-through");
    } else if (code === 39) {
      delete style.color;
    } else if (code === 49) {
      delete style.backgroundColor;
    } else if (code >= 30 && code <= 37) {
      style.color = ansiBaseColors[code - 30];
    } else if (code >= 90 && code <= 97) {
      style.color = ansiBrightColors[code - 90];
    } else if (code >= 40 && code <= 47) {
      style.backgroundColor = ansiBaseColors[code - 40];
    } else if (code >= 100 && code <= 107) {
      style.backgroundColor = ansiBrightColors[code - 100];
    } else if ((code === 38 || code === 48) && codes[i + 1] === 5) {
      const color = ansi256Color(codes[i + 2]);
      if (color) {
        if (code === 38) style.color = color;
        else style.backgroundColor = color;
      }
      i += 2;
    } else if ((code === 38 || code === 48) && codes[i + 1] === 2) {
      const [r, g, b] = [codes[i + 2], codes[i + 3], codes[i + 4]];
      if ([r, g, b].every((value) => Number.isFinite(value) && value >= 0 && value <= 255)) {
        const color = `rgb(${r}, ${g}, ${b})`;
        if (code === 38) style.color = color;
        else style.backgroundColor = color;
      }
      i += 4;
    }
  }
}

function mergeTextDecoration(value: CSSProperties["textDecorationLine"], next: string): CSSProperties["textDecorationLine"] {
  const parts = new Set(String(value || "").split(/\s+/).filter(Boolean));
  parts.add(next);
  return Array.from(parts).join(" ") as CSSProperties["textDecorationLine"];
}

function removeTextDecoration(value: CSSProperties["textDecorationLine"], target: string): CSSProperties["textDecorationLine"] | undefined {
  const parts = String(value || "").split(/\s+/).filter(Boolean).filter((part) => part !== target);
  return parts.length > 0 ? (parts.join(" ") as CSSProperties["textDecorationLine"]) : undefined;
}

function parseAnsiSegments(text: string): AnsiSegment[] {
  const normalized = normalizeTerminalText(text);
  const segments: AnsiSegment[] = [];
  const style: AnsiStyle = {};
  let lastIndex = 0;
  const pattern = /\x1b\[([0-9;?]*)([ -/]*)?([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\|$)|\x1b[ -/]*[@-~]/g;
  for (const match of normalized.matchAll(pattern)) {
    const index = match.index ?? 0;
    pushAnsiSegment(segments, normalized.slice(lastIndex, index), style);
    if (match[3] === "m") {
      applyAnsiCodes(style, match[1]);
    }
    lastIndex = index + match[0].length;
  }
  pushAnsiSegment(segments, normalized.slice(lastIndex), style);
  return segments;
}

export const statusColors: Record<string, string> = {
  running: "#f59e0b",
  in_progress: "#f59e0b",
  complete: "#22c55e",
  success: "#22c55e",
  failed: "#ef4444",
  error: "#ef4444",
};

export const terminalFontFamily =
  '"Cascadia Mono", "Cascadia Code", Consolas, "Microsoft YaHei Mono", "Microsoft YaHei", "Noto Sans Mono CJK SC", monospace';

export function AnsiOutput({ text, onRendered }: { text: string; onRendered?: () => void }) {
  const segments = useMemo(() => parseAnsiSegments(text), [text]);

  useLayoutEffect(() => {
    onRendered?.();
  }, [onRendered, segments]);

  return (
    <div
      className="mindfs-ansi-output"
      style={{
        marginTop: "10px",
        padding: "8px",
        borderRadius: "8px",
        background: "rgba(148, 163, 184, 0.12)",
        border: "1px solid rgba(148, 163, 184, 0.28)",
        overflowX: "auto",
        overflowY: "hidden",
        width: "100%",
        boxSizing: "border-box",
        WebkitOverflowScrolling: "touch",
      }}
    >
      <pre
        style={{
          margin: 0,
          minWidth: "max-content",
          color: "var(--mindfs-code-text, var(--text-primary))",
          fontFamily: terminalFontFamily,
          fontSize: "12px",
          lineHeight: 1.6,
          tabSize: 8,
          whiteSpace: "pre",
        }}
      >
        {segments.map((segment, index) => (
          <span key={index} style={segment.style}>
            {segment.text}
          </span>
        ))}
      </pre>
    </div>
  );
}
