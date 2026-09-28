import React from "react";
import { MultiProjectSessionList } from "./SessionList";
import type { ProjectSessionGroup, SessionItem } from "./SessionList";
import { useBackLayer } from "../app/useBackNavigation";
import { useI18n } from "../i18n";

/**
 * 「已归档对话」视图：在**右栏自己的列里**升起，不做全屏浮层。
 *
 * 为什么不复用 BottomSheet / 不用 position:fixed 的遮罩：那一套是全视口的，
 * 会把主面板和左栏一起盖住。这里要的是「右栏内部浮起一层」—— 所以遮罩和面板
 * 都用 absolute，落在右栏那根 flex 列（AppShell 的 rightStyle 已带
 * position: relative）里。主面板和左栏一点不碰，遮罩底下的会话列表透出来。
 *
 * 收起按钮放在标题栏右侧并朝右：右栏已经被「关掉右栏」那条 rail 管着了，
 * 这里要的是退回上一级（会话列表），不是关栏。
 */

// 遮罩留出的顶部空白：面板比整栏短一截，透出底下的列表。
const TOP_GAP = 56;

type ArchivedSessionsPanelProps = {
  onClose: () => void;
  groups: ProjectSessionGroup[];
  loading?: boolean;
  selectedKey?: string;
  onSelect?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  emptyText?: React.ReactNode;
};

export function ArchivedSessionsPanel({
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
  // 挂载即打开（由调用方条件渲染），所以恒为 true。
  useBackLayer(true, onClose);

  // 没有归档的会话就什么都别画，别把空视图推给用户
  const total = groups.reduce((sum, g) => sum + (g.sessions?.length || 0), 0);

  return (
    // 定位上下文由调用方（占满右栏整列的 relative wrapper）提供。
    // 这一层必须 absolute 盖满整列：写成 flex item 会跟会话列表并排，
    // 变成「上下分栏」而不是「浮在上面」。
    <div
      data-archived-panel="layer"
      style={{
        position: "absolute",
        inset: 0,
        zIndex: 2,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
      }}
    >
      {/* 遮罩：半透明黑，盖住右栏自身内容，主面板/左栏不受影响 */}
      <div
        data-archived-panel="scrim"
        onClick={onClose}
        style={{
          position: "absolute",
          inset: 0,
          zIndex: 1,
          background: "rgba(0, 0, 0, 0.42)",
        }}
      />
      {/* 面板本体：上浮（translateY 从底部滑上来），盖在遮罩之上 */}
      <div
        data-archived-panel="sheet"
        style={{
          position: "absolute",
          left: 0,
          right: 0,
          bottom: 0,
          top: TOP_GAP,
          zIndex: 2,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          borderTopLeftRadius: "12px",
          borderTopRightRadius: "12px",
          boxShadow: "0 -8px 24px rgba(0, 0, 0, 0.22)",
          background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
          animation: "mindfs-archived-rise 0.24s cubic-bezier(0.2, 0.8, 0.2, 1)",
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
          d="M3 4.5A1.5 1.5 0 0 1 4.5 3h15A1.5 1.5 0 0 1 21 4.5V6H3zm-.5 3A1.5 1.5 0 0 0 1 8.5V19a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V8.5a2 2 0 0 0-1.5-1.5zm5 3.5a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-7a1 1 0 0 0-1-1zm2.5 3h5v2h-5z"
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
          <polyline points="3.5 8 8 12.5 12.5 8" />
        </svg>
      </button>
    </div>
  );
}
