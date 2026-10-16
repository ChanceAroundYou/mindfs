import React, { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { menuItemStyle } from "../../shared/styles";
import { SessionDirectoryPicker } from "./SessionDirectoryPicker";
import { useSessionDirExists } from "./useSessionDirBrowser";

/**
 * 会话标题栏右侧的 ⋯ 菜单。按钮形态照搬文件面板的 FileTreeHeaderOverlays，
 * 菜单项复用会话列表卡片的同一套（重命名/归档/删除）。
 *
 * 最后一项「修改目录」是这里独有的：下面再挂一行小字显示会话当前的工作目录
 * （长路径换行、目录不存在标红）。点它开选路径弹层。
 *
 * 两个置灰条件，都不是「点了才报错」：
 *   - 非 claude 会话：repoint 要 fork 转录，别的 agent 没有这条路径。
 *   - 会话正在回复：repoint 第一件事是掐掉 agent 进程，跑着的回合会被腰斩在半句上。
 */
export function SessionHeaderMenu({
  agent,
  rootId,
  nodeId,
  rootPath,
  workDir,
  archived,
  running,
  onRename,
  onArchive,
  onDelete,
  onRepoint,
}: {
  agent?: string;
  rootId?: string | null;
  nodeId?: string;
  rootPath?: string | null;
  /** 会话显式选定的工作目录；空 = 项目主 checkout。 */
  workDir?: string;
  archived?: boolean;
  /** 有回合在跑（presence !== idle）。换目录要掐进程，此时不许点。 */
  running?: boolean;
  /** 重命名：弹输入框与落库都在 app 层（视图层不许直接调 dialog 服务）。 */
  onRename: () => void;
  onArchive: (archived: boolean) => void;
  onDelete: () => void;
  /** 换目录：搬转录 + 清 related + 钉新 cwd，一次做完。失败时抛错，由这里显示。 */
  onRepoint: (targetPath: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [pickerError, setPickerError] = useState("");
  const menuRef = useRef<HTMLDivElement | null>(null);

  const canRepoint = String(agent || "") === "claude" && !!rootId && !running;
  // 置灰的原因要写出来，否则「点了没反应」比报错还费解。
  const repointBlockedReason = running ? t("session.changeDirRunning") : "";
  // 显示的是「这个会话在哪个目录里干活」：显式选过就是那个，没选过就是项目主 checkout。
  const displayDir = String(workDir || "").trim() || String(rootPath || "").trim();
  // 只有显式选过的目录才可能「不存在」——项目主 checkout 是注册表里登记着的，
  // 它没了整个会话都不在列表里。所以空 workDir 不必探测，省一次请求。
  const shouldProbeDir = !!String(workDir || "").trim() && !!displayDir;

  const dirMissing = useSessionDirExists(displayDir, menuOpen && shouldProbeDir, nodeId);

  useEffect(() => {
    if (!menuOpen) return;
    const onDocClick = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [menuOpen]);

  const closeMenu = () => setMenuOpen(false);

  const handleRepoint = async (targetPath: string) => {
    setBusy(true);
    setPickerError("");
    try {
      await onRepoint(targetPath);
      setPickerOpen(false);
    } catch (err) {
      setPickerError(err instanceof Error ? err.message : t("session.dirPicker.failed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div ref={menuRef} style={{ position: "relative", flexShrink: 0 }}>
        <button
          type="button"
          data-onboarding="session-menu"
          onClick={() => setMenuOpen((open) => !open)}
          aria-label={t("sessionList.menu")}
          title={t("sessionList.menu")}
          style={{
            width: "28px",
            height: "28px",
            borderRadius: "8px",
            border: "none",
            background: menuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
            color: "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: "pointer",
            outline: "none",
          }}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <circle cx="12" cy="5" r="1.8" />
            <circle cx="12" cy="12" r="1.8" />
            <circle cx="12" cy="19" r="1.8" />
          </svg>
        </button>
        {menuOpen ? (
          <div
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              right: 0,
              width: "min(140px, calc(100vw - 16px))",
              padding: "6px",
              borderRadius: "10px",
              border: "1px solid var(--border-color)",
              background: "var(--menu-bg)",
              boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
              zIndex: 20,
            }}
          >
            <button type="button" onClick={() => {
                closeMenu();
                onRename();
              }} style={menuItemStyle}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 20h9" />
                <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
              </svg>
              {t("sessionList.rename")}
            </button>
            <button
              type="button"
              onClick={() => {
                closeMenu();
                onArchive(!archived);
              }}
              style={menuItemStyle}
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <rect x="3" y="4" width="18" height="4" rx="1" />
                <path d="M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8" />
                <path d="M10 12h4" />
              </svg>
              {archived ? t("sessionList.unarchive") : t("sessionList.archive")}
            </button>
            <button
              type="button"
              onClick={() => {
                closeMenu();
                onDelete();
              }}
              style={{ ...menuItemStyle, color: "#dc2626" }}
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6l-1 14H6L5 6" />
                <path d="M10 11v6" />
                <path d="M14 11v6" />
                <path d="M9 6V4h6v2" />
              </svg>
              {t("common.delete")}
            </button>

            <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />

            <button
              type="button"
              disabled={!canRepoint}
              title={repointBlockedReason || undefined}
              onClick={() => {
                if (!canRepoint) return;
                closeMenu();
                setPickerError("");
                setPickerOpen(true);
              }}
              style={{
                ...menuItemStyle,
                color: canRepoint ? "var(--text-primary)" : "var(--text-secondary)",
                opacity: canRepoint ? 1 : 0.55,
                cursor: canRepoint ? "pointer" : "not-allowed",
              }}
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
              </svg>
              {t("session.changeDir")}
            </button>
            {displayDir ? (
              <div
                style={{
                  padding: "0 10px 6px",
                  fontSize: "11px",
                  lineHeight: 1.35,
                  color: dirMissing ? "#dc2626" : "var(--text-secondary)",
                  overflowWrap: "anywhere",
                  whiteSpace: "pre-wrap",
                }}
              >
                {displayDir}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      {pickerOpen ? (
        <SessionDirectoryPicker
          nodeId={nodeId}
          defaultPath={displayDir}
          busy={busy}
          error={pickerError}
          onCancel={() => {
            if (!busy) setPickerOpen(false);
          }}
          onConfirm={(path) => void handleRepoint(path)}
        />
      ) : null}
    </>
  );
}
