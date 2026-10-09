import type { ToolCallContentItem, ToolCallLocation } from "../../services/session";

export type DetailSection =
  | { type: "diff"; path: string; markdown: string }
  | { type: "text"; markdown: string };

function stripAnsi(text: string): string {
  return text
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\|$)/g, "")
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\x1b[ -/]*[@-~]/g, "");
}

function prefixDiffLines(text: string, prefix: "+" | "-"): string[] {
  return text.split("\n").map((line) => `${prefix}${line}`);
}

function renderStructuredDiff(path: string, oldText?: string, newText?: string): string {
  const lines: string[] = [`--- a/${path}`, `+++ b/${path}`];
  if (typeof oldText === "string" && oldText !== "") {
    lines.push(...prefixDiffLines(oldText, "-"));
  }
  if (typeof newText === "string" && newText !== "") {
    lines.push(...prefixDiffLines(newText, "+"));
  }
  return `~~~diff\n${lines.join("\n")}\n~~~`;
}

function renderAddedText(path: string, text: string): string {
  return renderStructuredDiff(path, undefined, text);
}

function renderDeletedText(path: string, text: string): string {
  return renderStructuredDiff(path, text, undefined);
}

export function isDiffLikeText(text: string): boolean {
  const trimmed = stripAnsi(text).trim();
  if (!trimmed) return false;
  if (/^(```|~~~)/.test(trimmed)) return false;
  return /^(diff --git|index |--- |\+\+\+ |@@ )/m.test(trimmed);
}

export function extractDiffPath(text: string, fallbackPath = "(unknown)"): string {
  const lines = stripAnsi(text).split("\n");
  for (const line of lines) {
    const match = line.match(/^\+\+\+\s+(?:b\/)?(.+)$/);
    if (match?.[1]) return match[1].trim();
  }
  for (const line of lines) {
    const match = line.match(/^diff --git a\/.+ b\/(.+)$/);
    if (match?.[1]) return match[1].trim();
  }
  return fallbackPath;
}

export function normalizeDisplayPath(path: string, rootPath?: string): string {
  const normalizedPath = (path || "").replace(/\\/g, "/").trim();
  const normalizedRoot = (rootPath || "").replace(/\\/g, "/").replace(/\/+$/g, "").trim();
  if (!normalizedPath || !normalizedRoot) {
    return path;
  }
  if (normalizedPath === normalizedRoot) {
    return ".";
  }
  if (normalizedPath.startsWith(`${normalizedRoot}/`)) {
    return normalizedPath.slice(normalizedRoot.length + 1);
  }
  return path;
}

export function buildDetailSections(content?: ToolCallContentItem[], locations?: ToolCallLocation[], rootPath?: string): DetailSection[] {
  if (!content || content.length === 0) return [];
  const sections: DetailSection[] = [];
  let locationIndex = 0;
  for (const item of content) {
    if (item.type === "diff") {
      const path = normalizeDisplayPath(item.path || locations?.[locationIndex]?.path || "(unknown)", rootPath);
      sections.push({ type: "diff", path, markdown: renderStructuredDiff(path, item.oldText, item.newText) });
      locationIndex += 1;
      continue;
    }
    if (item.type === "text" && item.text?.trim()) {
      const fallbackPath = normalizeDisplayPath(locations?.[locationIndex]?.path || "(unknown)", rootPath);
      const path = normalizeDisplayPath(item.path || fallbackPath, rootPath);
      if (item.changeKind === "add") {
        sections.push({ type: "diff", path, markdown: renderAddedText(path, item.text) });
        locationIndex += 1;
        continue;
      }
      if (item.changeKind === "delete") {
        sections.push({ type: "diff", path, markdown: renderDeletedText(path, item.text) });
        locationIndex += 1;
        continue;
      }
      if (isDiffLikeText(item.text)) {
        sections.push({
          type: "diff",
          path: normalizeDisplayPath(extractDiffPath(item.text, fallbackPath), rootPath),
          markdown: `~~~diff\n${item.text.trim()}\n~~~`,
        });
        locationIndex += 1;
      } else {
        sections.push({ type: "text", markdown: item.text });
      }
    }
  }
  return sections;
}
