import React, { useEffect, useState } from "react";
import { MultiProjectSessionList } from "./SessionList";
import type {
  ProjectSessionGroup,
  SessionItem,
} from "./SessionList";
import { useBackLayer } from "../app/useBackNavigation";
import { useI18n } from "../i18n";

/**
 * 「已归档对话」浮动面板。
 *
 * 刻意**不**复用 BottomSheet：那一套是为会话抽屉做的（拖拽改高 / 双击全屏 / 顶部句柄条），
 * 归档面板要的是「从底部升起来 + 一层遮罩 + 一个收起按钮」，语义不同，硬套会带进来
 * 一堆用不上的手势。ponytail: 想要拖拽高度再加，别在没需求时先做。
 *
 * 高度留出顶部一块空白（TOP_GAP）而不是满屏：满屏会和主面板糊成一片，看不出这是浮层。
 */

const TOP_GAP = 84;

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
  const [isMounted, setIsMounted] = useState(isOpen);
  const [isAnimating, setIsAnimating] = useState(false);

  // 返回键 / 边缘侧滑收起这一层（和 PanelShell、BottomSheet 走同一个栈）
  useBackLayer(isOpen, onClose);

  useEffect(() => {
    if (isOpen) {
      setIsMounted(true);
      setIsAnimating(true);
      return;
    }
    if (!isMounted) return;
    setIsAnimating(true);
  }, [isOpen, isMounted]);

  // 关闭动画走完再卸载，否则收起按钮一点面板就凭空消失，没有「沉下去」这一下
  useEffect(() => {
    if (!isAnimating) return;
    if (isOpen) return;
    const timer = window.setTimeout(() => {
      setIsAnimating(false);
      setIsMounted(false);
    }, 240);
    return () => window.clearTimeout(timer);
  }, [isAnimating, isOpen]);

  if (!isMounted && !isAnimating) return null;

  return (
    <>
      {/* 遮罩：点它也能收起，但右上角另给一个显式按钮，别逼用户去猜 */}
      <div
        data-archived-panel="scrim"
        onClick={onClose}
        style={{
          position: "fixed",
          inset: 0,
          background: "rgba(0, 0, 0, 0.32)",
          zIndex: 1100,
          opacity: isOpen ? 1 : 0,
          transition: "opacity 0.24s ease-out",
          pointerEvents: isOpen ? "auto" : "none",
        }}
      />
      <button
        type="button"
        data-archived-panel="collapse"
        aria-label={t("sessionList.archivedPanel.collapse")}
        title={t("sessionList.archivedPanel.collapse")}
        onClick={onClose}
        style={{
          position: "fixed",
          top: 16,
          right: 16,
          zIndex: 1102,
          width: 30,
          height: 30,
          borderRadius: "9px",
          border: "none",
          padding: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          background: "var(--panel-bg, #ffffff)",
          color: "var(--text-secondary)",
          boxShadow: "0 2px 10px rgba(0, 0, 0, 0.18)",
          opacity: isOpen ? 1 : 0,
          transform: isOpen ? "translateY(0)" : "translateY(-8px)",
          transition:
            "opacity 0.2s ease-out, transform 0.2s ease-out, background 0.15s ease",
          pointerEvents: isOpen ? "auto" : "none",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = "var(--mindfs-hover-bg, rgba(0,0,0,0.06))";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = "var(--panel-bg, #ffffff)";
        }}
      >
        <svg
          width="16"
          height="16"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <polyline points="3.5 6 8 10.5 12.5 6" />
        </svg>
      </button>

      <div
        data-archived-panel="sheet"
        style={{
          position: "fixed",
          left: 0,
          right: 0,
          bottom: 0,
          height: `calc(100vh - ${TOP_GAP}px)`,
          zIndex: 1101,
          background: "var(--panel-bg, #ffffff)",
          color: "var(--text-primary)",
          borderRadius: "16px 16px 0 0",
          boxShadow: "0 -4px 24px rgba(0, 0, 0, 0.08)",
          borderTop: "1px solid rgba(148, 163, 184, 0.22)",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          transition: "transform 0.24s cubic-bezier(0.4, 0, 0.2, 1)",
          transform: isOpen ? "translateY(0)" : "translateY(100%)",
          pointerEvents: isOpen ? "auto" : "none",
        }}
        onTransitionEnd={() => {
          if (!isOpen) setIsAnimating(false);
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: "6px",
            height: "34px",
            flexShrink: 0,
            borderBottom: "1px solid var(--border-color)",
            fontSize: "12px",
            fontWeight: 600,
            color: "var(--text-secondary)",
          }}
        >
          {t("sessionList.archivedPanel.title")}
          <span style={{ fontWeight: 400, opacity: 0.75 }}>
            {groups.reduce((sum, g) => sum + (g.sessions?.length || 0), 0)}
          </span>
        </div>
        <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
          <MultiProjectSessionList
            groups={groups}
            selectedKey={selectedKey}
            loading={loading}
            emptyText={emptyText ?? t("sessionList.archivedPanel.empty")}
            onSelect={onSelect}
            onArchive={onArchive}
          />
        </div>
      </div>
    </>
  );
}
