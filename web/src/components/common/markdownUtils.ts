export function normalizePosixPath(input: string): string {
  const absolute = input.startsWith("/");
  const parts = input.split("/").filter((part) => part && part !== ".");
  const normalized: string[] = [];
  for (const part of parts) {
    if (part === "..") {
      normalized.pop();
      continue;
    }
    normalized.push(part);
  }
  return absolute ? `/${normalized.join("/")}` : normalized.join("/");
}

export function dirnamePosix(input: string): string {
  const normalized = normalizePosixPath(input.replace(/\\/g, "/"));
  if (!normalized || !normalized.includes("/")) return ".";
  const parts = normalized.split("/");
  parts.pop();
  return parts.join("/") || ".";
}

export function resolveMarkdownHref(currentPath: string, href: string): string {
  const trimmed = href.trim();
  if (!trimmed) return "";
  if (trimmed.startsWith("file://")) {
    return decodeURIComponent(trimmed.slice("file://".length));
  }
  if (trimmed.startsWith("/")) {
    return decodeURIComponent(trimmed);
  }
  const baseDir = currentPath ? dirnamePosix(currentPath) : ".";
  return decodeURIComponent(normalizePosixPath(`${baseDir}/${trimmed}`));
}

export function isExternalHref(href: string): boolean {
  return /^(https?:|mailto:|tel:)/i.test(href);
}

export function isDirectImageSrc(src: string): boolean {
  return /^(https?:|data:|blob:)/i.test(src);
}

export function normalizeMarkdownMathDelimiters(content: string): string {
  const lines = content.split("\n");
  let fenced = false;
  let fenceMarker = "";

  return lines
    .map((line) => {
      const fenceMatch = /^(\s*)(`{3,}|~{3,})/.exec(line);
      if (fenceMatch) {
        const marker = fenceMatch[2][0];
        if (!fenced) {
          fenced = true;
          fenceMarker = marker;
        } else if (marker === fenceMarker) {
          fenced = false;
          fenceMarker = "";
        }
        return line;
      }

      if (fenced) return line;

      const trimmed = line.trim();
      if (trimmed === "\\[" || trimmed === "\\]") {
        return `${line.slice(0, line.indexOf(trimmed))}$$`;
      }

      return line.replace(/\\\((.+?)\\\)/g, (_match, formula: string) => `$${formula}$`);
    })
    .join("\n");
}
