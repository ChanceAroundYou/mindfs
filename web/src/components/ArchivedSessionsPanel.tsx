import React, { useEffect, useState } from "react";
import { MultiProjectSessionList } from "./SessionList";
import type { ProjectSessionGroup, SessionItem } from "./SessionList";
import { useBackLayer } from "../app/useBackNavigation";
import { useI18n } from "../i18n";

/**
 * 「已归档对话」视图：在**右栏自己的列里**升起，不做全屏浮层。
 *
 * 为什么不复用 BottomSheet / 不再用 position:fixed 的遮罩：那一套是全视口的，
 * 会把主面板和左栏一起盖住。这里要的是「右栏内部换了个视图」—— 所以本组件
 * **不产出任何 fixed 定位、不产出遮罩**，它就是右栏 flex 列里的一个普通兄弟节点，
 * 靠高度 0 / 100% 切换。AppShell 已经给右栏做了侧栏动画和拖宽，本组件不重复。
 *
 * 收起按钮放在标题栏右侧：右栏已经被「关掉右栏」那条 rail 管着了，这里要的是
 * 退回上一级（会话列表），不是关栏。
 */

type ArchivedSessionsPanelProps = {
  isOpen: boolean;
  onClose: () => void;
  groups: ProjectSessionGroup[];
  loading?: boolean;
  selectedKey?: string;
  onSelect?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  emptyText?: React.ReactNode;
};

export function ArchivedSessionsPanel({
  isOpen,
  onClose,
  groups,
  loading = false,
  selectedKey = "",
  onSelect,
  onArchive,
  emptyText,
}: ArchivedSessionsPanelProps) {
  const { t } = useI18n();
  // 返回键 / 边缘侧滑退回会话列表：视图虽然不遮挡什么，但它确实占了右栏的
  // 一级位置，留在返回栈里才和右栏那条 rail 的行为一致。
  useBackLayer(isOpen, onClose);
  // 未打开时完全不在 DOM 里：右栏本来就常驻，留个空 div 只会占布局
  if (!isOpen) return null;

  // 没有归档的会话就什么都别画，别把空视图推给用户
  const total = groups.reduce((sum, g) => sum + (g.sessions?.length || 0), 0);
  if (groups.length === 0) {
    return (
      <div
        data-archived-panel="sheet"
        style={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          flexDirection: "column",
          background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
        }}
      >
        <ArchivedHeader
          title={t("sessionList.archivedPanel.title")}
          count={0}
          onClose={onClose}
        />
        <div
          style={{
            flex: 1,
            minHeight: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: "18px",
            textAlign: "center",
            fontSize: "12px",
            lineHeight: 1.6,
            color: "var(--text-secondary)",
          }}
        >
          {emptyText ?? t("sessionList.archivedPanel.empty")}
        </div>
      </div>
    );
  }

  return (
    <div
      data-archived-panel="sheet"
      style={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
      }}
    >
      <ArchivedHeader
        title={t("sessionList.archivedPanel.title")}
        count={total}
        onClose={onClose}
      />
      <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
        <MultiProjectSessionList
          groups={groups}
          selectedKey={selectedKey}
          loading={loading}
          emptyText={emptyText ?? t("sessionList.archivedPanel.empty")}
          hideHeader
          onSelect={onSelect}
          onArchive={onArchive}
        />
      </div>
    </div>
  );
}

function ArchivedHeader({
  title,
  count,
  onClose,
}: {
  title: string;
  count: number;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <div
      data-archived-panel="header"
      style={{
        display: "flex",
        alignItems: "center",
        gap: "6px",
        padding: "0 8px 0 10px",
        height: "44px",
        flexShrink: 0,
        borderBottom: "1px solid var(--border-color)",
        boxSizing: "border-box",
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        aria-hidden="true"
        style={{ color: "var(--text-secondary)", flexShrink: 0 }}
      >
        <path
          fill="currentColor"
          d="M3 4.5A1.5 1.5 0 0 1 4.5 3h15A1.5 1.5 0 0 1 21 4.5V6H3zm-.5 3A1.5 1.5 0 0 0 1 8.5V19a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V8.5a1.5 1.5 0 0 0-1.5-1.5zm5 3.5a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-7a1 1 0 0 0-1-1zm2.5 3h5v2h-5z"
        />
      </svg>
      <span
        style={{
          flex: 1,
          minWidth: 0,
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {title}
      </span>
      <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{count}</span>
      <button
        type="button"
        data-archived-panel="collapse"
        aria-label={t("sessionList.archivedPanel.collapse")}
        title={t("sessionList.archivedPanel.collapse")}
        onClick={onClose}
        style={{
          width: "26px",
          height: "26px",
          minWidth: "26px",
          border: "none",
          borderRadius: "7px",
          padding: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          background: "transparent",
          color: "var(--text-secondary)",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = "rgba(0,0,0,0.06)";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = "transparent";
        }}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <polyline points="8 3.5 12.5 8 8 12.5" />
        </svg>
      </button>
    </div>
  );
}
