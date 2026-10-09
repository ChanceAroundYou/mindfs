import React from "react";

export const toolIcons: Record<string, string> = {
  delete: "🗑️",
  move: "📦",
  think: "💭",
  fetch: "🌐",
  ask_user: "❓",
  todo: "✅",
  switch_mode: "🔁",
  other: "🔧",
};

export function renderToolIcon(kind: string, color?: string): React.ReactNode {
  if (kind === "read") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 32 32" aria-hidden="true" style={{ color: "#2f80ed" }}>
        <path fill="currentColor" d="M5 6C3.346 6 2 7.346 2 9v12c0 1.654 1.346 3 3 3l6.184-.02c.99 0 1.949.31 2.773.86L16 26.2l2.043-1.361a5 5 0 0 1 2.773-.84H27c1.654 0 3-1.346 3-3V9c0-1.654-1.346-3-3-3h-6.184c-1.386 0-2.73.408-3.882 1.176L16 7.799l-.934-.623A7 7 0 0 0 11.184 6zm0 2h6.184c.99 0 1.949.29 2.773.84L16 10.2l2.043-1.361A5 5 0 0 1 20.816 8H27c.552 0 1 .449 1 1v12c0 .551-.448 1-1 1h-6.184c-1.386 0-2.73.408-3.882 1.176l-.934.623l-.934-.623A7 7 0 0 0 11.184 22H5c-.552 0-1-.449-1-1V9c0-.551.448-1 1-1m1 4v2h8v-2zm12 0v2h8v-2zM6 16v2h8v-2zm12 0v2h8v-2z"/>
      </svg>
    );
  }
  if (kind === "task") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
        <path
          fill="none"
          stroke="#7dc4e4"
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M14.5 11.752L8 15.5l-6.5-3.752l.002-7.5L8 .5l6.5 3.752zM1.5 4.25L8 8m6.5-3.75L8 8m.003 0v7.5"
          strokeWidth="1"
        />
      </svg>
    );
  }
  if (kind === "todo") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 48 48" aria-hidden="true">
        <path fill="#3f51b5" d="m17.8 18.1l-7.4 7.3l-4.2-4.1L4 23.5l6.4 6.4l9.6-9.6zm0-13l-7.4 7.3l-4.2-4.1L4 10.5l6.4 6.4L20 7.3zm0 26l-7.4 7.3l-4.2-4.1L4 36.5l6.4 6.4l9.6-9.6z" />
        <path fill="#90caf9" d="M24 22h20v4H24zm0-13h20v4H24zm0 26h20v4H24z" />
      </svg>
    );
  }
  if (kind === "ask_user") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" style={{ color: "#ef4444" }}>
        <g fill="currentColor">
          <path d="M8 11a.75.75 0 1 1 0 1.5a.75.75 0 0 1 0-1.5m0-7c1.262 0 2.25.988 2.25 2.25c0 1.083-.566 1.648-1.021 2.104c-.408.407-.729.728-.729 1.396a.5.5 0 0 1-1 0c0-1.083.566-1.648 1.021-2.104c.408-.407.729-.728.729-1.396C9.25 5.538 8.712 5 8 5s-1.25.538-1.25 1.25a.5.5 0 0 1-1 0C5.75 4.988 6.738 4 8 4" />
          <path fillRule="evenodd" d="M8 1a7 7 0 0 1 6.999 7.001a7 7 0 0 1-10.504 6.06l-2.728.91a.582.582 0 0 1-.744-.714l.83-2.906A7 7 0 0 1 8 1m.001 1.001c-3.308 0-6 2.692-6 6c0 1.003.252 1.996.73 2.871l.196.36l-.726 2.54l1.978-.659l.428-.143l.39.226A6 6 0 0 0 8 14l.001.001c3.308 0 6-2.692 6-6s-2.692-6-6-6" clipRule="evenodd" />
        </g>
      </svg>
    );
  }
  if (kind === "search" || kind === "web_search") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 50 50" aria-hidden="true">
        <g fill="none" strokeLinecap="round" strokeLinejoin="round">
          <path stroke="#344054" strokeWidth="2" d="M41.667 41.667L31.146 31.146" />
          <path stroke="#344054" strokeWidth="3" d="m42.708 42.708l-7.291-7.291" />
          <path stroke="#306cfe" strokeWidth="2" d="M20.833 35.417c8.055 0 14.584-6.53 14.584-14.584S28.887 6.25 20.833 6.25S6.25 12.78 6.25 20.833c0 8.055 6.53 14.584 14.583 14.584" />
        </g>
      </svg>
    );
  }
  if (kind === "edit") {
    const themed = String(color || "").trim() !== "";
    const themedColor = themed ? String(color).trim() : "";
    if (themed) {
      return (
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 80 80" aria-hidden="true" style={{ color: themedColor }}>
          <g fill="none">
            <path fill="currentColor" stroke="currentColor" strokeLinejoin="round" strokeWidth="1.6" d="M38.4 22.742a2 2 0 1 0 0-4zm23.6 19.6a2 2 0 1 0-4 0zm-52-19.6v44h4v-44zm4 48h44v-4H14zm24.4-52H14v4h24.4zm23.6 48v-24.4h-4v24.4zm-4 4a4 4 0 0 0 4-4h-4zm-48-4a4 4 0 0 0 4 4v-4zm4-44v-4a4 4 0 0 0-4 4z" />
            <path fill="currentColor" stroke="currentColor" strokeLinejoin="round" strokeWidth="1.4" fillRule="evenodd" d="M68.015 21.897c.78-.78.78-2.044 0-2.824l-5.657-5.657a2.003 2.003 0 0 0-2.833 0L30.7 42.242a16 16 0 0 0-4.555 9.267l-.308 2.384l-.125.974a.758.758 0 0 0 .848.849l.975-.126l2.384-.307a16 16 0 0 0 9.266-4.555z" clipRule="evenodd" />
            <path stroke="currentColor" strokeLinejoin="round" strokeWidth="5.2" d="m52.147 20.804l8.48 8.48" />
          </g>
        </svg>
      );
    }
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 80 80" aria-hidden="true">
        <g fill="none">
          <path fill="#2f80ed" stroke="#2f80ed" strokeLinejoin="round" strokeWidth="1.6" d="M38.4 22.742a2 2 0 1 0 0-4zm23.6 19.6a2 2 0 1 0-4 0zm-52-19.6v44h4v-44zm4 48h44v-4H14zm24.4-52H14v4h24.4zm23.6 48v-24.4h-4v24.4zm-4 4a4 4 0 0 0 4-4h-4zm-48-4a4 4 0 0 0 4 4v-4zm4-44v-4a4 4 0 0 0-4 4z" />
          <path fill="#9b51e0" stroke="#9b51e0" strokeLinejoin="round" strokeWidth="1.4" fillRule="evenodd" d="M68.015 21.897c.78-.78.78-2.044 0-2.824l-5.657-5.657a2.003 2.003 0 0 0-2.833 0L30.7 42.242a16 16 0 0 0-4.555 9.267l-.308 2.384l-.125.974a.758.758 0 0 0 .848.849l.975-.126l2.384-.307a16 16 0 0 0 9.266-4.555z" clipRule="evenodd" />
          <path stroke="#f2c94c" strokeLinejoin="round" strokeWidth="5.2" d="m52.147 20.804l8.48 8.48" />
        </g>
      </svg>
    );
  }
  if (kind === "execute") {
    return (
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 80 80" aria-hidden="true">
        <g fill="none" strokeLinecap="round" strokeLinejoin="round" strokeWidth="5.4">
          <path stroke="#9b51e0" d="m15 24l17.16 15.253a1 1 0 0 1 0 1.494L15 56" />
          <path stroke="#2f80ed" d="M65 56H41" />
        </g>
      </svg>
    );
  }
  return toolIcons[kind] || toolIcons.other;
}
