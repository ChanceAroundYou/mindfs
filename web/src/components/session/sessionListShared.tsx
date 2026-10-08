import React from "react";
import { useI18n } from "../../i18n/index";
import type { ForkSessionSource, SessionItem } from "./sessionListTypes";

export const COLLAPSED_CHILD_SESSION_LIMIT = 3;
export const MULTI_PROJECT_VISIBLE_LIMIT = 6;
export const MAIN_SESSION_ICON_OFFSET = "2px";
export const SUB_SESSION_ICON_OFFSET = "0px";

export function ToggleRowButton({
  label,
  loading,
  marginLeft,
  showExpandIcon = false,
  showCollapseIcon = false,
  onClick,
  onCollapse,
}: {
  label: string;
  loading?: boolean;
  marginLeft: string;
  showExpandIcon?: boolean;
  showCollapseIcon?: boolean;
  onClick: () => void;
  onCollapse?: () => void;
}) {
  const { t } = useI18n();
  const icon = (rotate = false) => (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="1em"
      height="1em"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{
        width: "13px",
        height: "13px",
        flexShrink: 0,
        transform: rotate ? "rotate(180deg)" : "none",
      }}
    >
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" d="M12.146 7.146a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 11.293zm0-4a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 7.293z" />
    </svg>
  );

  // 展开态这一行有两个动作：拉下一批子会话 / 全部收起。所以收起图标是**整行
  // 可点**的 —— 它就长在「展开 N 条」这一行里，点这一行任何位置都该能收回去。
  // 行内不再套一个可点的子元素：嵌套 button 是非法 HTML，浏览器会把子 button
  // 从父 button 里拆出来，事件到不了父级，再叠上 stopPropagation，那个收起图标
  // 就永远按不动（只能继续展开）。所以收起 = 整行换 onCollapse，不是第二个按钮。
  const collapsed = showCollapseIcon && !!onCollapse;
  return (
    <button
      type="button"
      disabled={loading}
      onClick={collapsed ? onCollapse : onClick}
      title={collapsed ? t("common.collapse") : undefined}
      style={{
        marginLeft,
        marginTop: "-2px",
        border: "none",
        background: "transparent",
        color: "var(--text-primary)",
        borderRadius: 0,
        padding: 0,
        minHeight: "13px",
        width: `calc(100% - ${marginLeft})`,
        boxSizing: "border-box",
        cursor: loading ? "default" : "pointer",
        fontSize: "11px",
        lineHeight: 1.1,
        textAlign: "center",
        opacity: loading ? 0.6 : 1,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", gap: "6px", minWidth: 0, flexShrink: 1 }}>
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{label}</span>
        {showExpandIcon ? icon(false) : null}
        {showCollapseIcon ? icon(true) : null}
      </span>
    </button>
  );
}

export function PinIcon({ pinned }: { pinned: boolean }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="m16.219 4.838l2.964 2.967c2.012 2.014 3.018 3.021 2.784 4.107c-.235 1.085-1.567 1.585-4.23 2.586l-1.845.693c-.713.268-1.07.402-1.345.64q-.181.158-.322.352c-.212.297-.313.664-.515 1.4c-.46 1.672-.69 2.508-1.239 2.821c-.23.132-.492.2-.758.2c-.63 0-1.243-.614-2.469-1.84l-1.466-1.468l-1.079-1.08L5.285 14.8c-1.218-1.219-1.827-1.828-1.83-2.455a1.53 1.53 0 0 1 .203-.773c.313-.543 1.143-.772 2.803-1.23c.737-.203 1.105-.304 1.402-.517q.199-.144.36-.332c.236-.278.368-.637.63-1.355l.669-1.823c.987-2.693 1.48-4.04 2.568-4.28s2.102.774 4.129 2.803"
        clipRule="evenodd"
        opacity={pinned ? 1 : 0.5}
      />
      <path fill="currentColor" d="m3.302 21.776l4.476-4.48l-1.079-1.08l-4.476 4.48a.764.764 0 0 0 1.08 1.08" />
    </svg>
  );
}

export function ArchiveIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="1em"
      height="1em"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        d="M3 4.5A1.5 1.5 0 0 1 4.5 3h15A1.5 1.5 0 0 1 21 4.5V6H3zm-.5 3A1.5 1.5 0 0 0 1 8.5V19a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V8.5a1.5 1.5 0 0 0-1.5-1.5zm5 3.5a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-7a1 1 0 0 0-1-1zm2.5 3h5v2h-5z"
      />
    </svg>
  );
}

/**
 * 顶栏归档入口：只留图标，尺寸与旁边的搜索按钮一致（34×34）。
 * 2026-09-29 用户要求从列表末尾挪到搜索图标右边 —— 原来挂在列表里，不管
 * 吸不吸底都跟着项目列表跑，得滚动才看得到。两个列表组件（单项目 / 多项目）
 * 各有一个顶栏且互斥显示，抽成组件给两处共用。
 */
export function ArchiveHeaderButton({ onOpen }: { onOpen?: () => void }) {
  const { t } = useI18n();
  if (!onOpen) return null;
  return (
      <button
        type="button"
        data-archive-entry="open"
        aria-label={t("sessionList.archive")}
        title={t("sessionList.archive")}
        onClick={onOpen}
        style={{
          width: "34px",
          height: "34px",
          minWidth: "34px",
          border: "none",
          borderRadius: "8px",
          padding: 0,
          background: "transparent",
          color: "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          transition: "all 0.15s ease",
        }}
      >
        <span style={{ fontSize: "15px", lineHeight: 1, display: "inline-flex" }}>
          <ArchiveIcon />
        </span>
      </button>
  );
}

export function shellBadgeLabel(shell?: string): string {
  const normalized = String(shell || "").trim().replace(/\\/g, "/");
  const base = (normalized.split("/").filter(Boolean).pop() || normalized || "sh").toLowerCase();
  if (base === "powershell.exe") return "ps";
  if (base === "pwsh.exe") return "pwsh";
  if (base === "cmd.exe") return "cmd";
  if (base.endsWith(".exe")) return base.slice(0, -4);
  return base;
}

export function parseForkSessionSource(source?: string): ForkSessionSource | null {
  const value = String(source || "").trim();
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as {
      type?: unknown;
      session_key?: unknown;
      seq?: unknown;
    };
    if (parsed?.type !== "fork") return null;
    return {
      sessionKey: String(parsed.session_key || "").trim(),
      seq: Number(parsed.seq || 0),
    };
  } catch {
    if (!value.includes('"type":"fork"') && !value.includes('"type": "fork"')) {
      return null;
    }
    return { sessionKey: "", seq: 0 };
  }
}

export function forkSessionDisplayName(
  storedName: string,
  source: ForkSessionSource | null,
  sessionByKey: Map<string, SessionItem>,
  rootId?: string,
): string {
  // The persisted name is authoritative, including user edits to fork titles.
  if (storedName.trim()) return storedName;
  if (!source) return storedName;
  const parentName = String(
    sessionByKey.get(rootId ? `${rootId}:${source.sessionKey}` : source.sessionKey)?.name ||
      sessionByKey.get(source.sessionKey)?.name ||
      "",
  ).trim();
  const base = parentName || "Fork";
  return source.seq > 0 ? `${base}#${source.seq}` : base;
}

export function isSessionSyncing(
  session: SessionItem,
  syncingSessionKeys?: Set<string>,
): boolean {
  if (!syncingSessionKeys || syncingSessionKeys.size === 0) {
    return false;
  }
  const key = session.key || session.session_key || "";
  if (!key) {
    return false;
  }
  const rootId = session.root_id || "";
  const nodeId = String((session as any)?._nodeId || "").trim();
  return (
    syncingSessionKeys.has(key) ||
    (!!rootId && syncingSessionKeys.has(`${rootId}::${key}`)) ||
    (!!rootId && !!nodeId && syncingSessionKeys.has(`${nodeId}::${rootId}::${key}`))
  );
}
