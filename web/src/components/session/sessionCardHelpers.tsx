import React from "react";
import type { Locale } from "../../i18n/index";

export function formatTime(isoString: string, locale: Locale, justNow: string): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  const now = new Date();
  const diff = now.getTime() - date.getTime();
  if (diff < 60000) return justNow;
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h`;
  if (now.getFullYear() === date.getFullYear()) {
    return new Intl.DateTimeFormat(locale, { month: "numeric", day: "numeric" }).format(date);
  }
  return new Intl.DateTimeFormat(locale, { year: "2-digit", month: "numeric" }).format(date);
}

export { iconButtonStyle, menuItemStyle, inlineActionStyle } from "../../shared/styles";

export function ChevronLeftIcon() {
  return (
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
      <path d="m15 18-6-6 6-6" />
    </svg>
  );
}

export function renderHighlightedText(
  text: string,
  query?: string,
  palette?: { color?: string },
): React.ReactNode {
  const source = String(text || "");
  const needle = String(query || "").trim();
  if (!source || !needle) {
    return source;
  }
  const lowerSource = source.toLowerCase();
  const lowerNeedle = needle.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;

  for (;;) {
    const index = lowerSource.indexOf(lowerNeedle, cursor);
    if (index < 0) {
      break;
    }
    if (index > cursor) {
      parts.push(source.slice(cursor, index));
    }
    const match = source.slice(index, index + needle.length);
    parts.push(
      <mark
        key={`${index}:${match}`}
        style={{
          background: "transparent",
          color: "var(--accent-color)",
          padding: 0,
        }}
      >
        {match}
      </mark>,
    );
    cursor = index + needle.length;
  }

  if (cursor < source.length) {
    parts.push(source.slice(cursor));
  }
  return parts.length ? parts : source;
}

export function SubSessionIcon({ color }: { color?: string }) {
  const c = String(color || "").trim() || "var(--accent-color)";
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="18"
      height="18"
      viewBox="0 0 32 32"
      aria-hidden="true"
      style={{ color: c, display: "block" }}
    >
      <path d="M0 0h32v32H0z" fill="none" />
      <path
        fill="currentColor"
        d="M23 20c-2.41 0-4.43 1.72-4.9 4H14c-2.21 0-4-1.79-4-4v-8.1A5 5 0 1 0 4 7c0 2.41 1.72 4.43 4 4.9V20c0 3.31 2.69 6 6 6h4.1a5 5 0 1 0 4.9-6M6 7c0-1.65 1.35-3 3-3s3 1.35 3 3s-1.35 3-3 3s-3-1.35-3-3"
      />
    </svg>
  );
}
