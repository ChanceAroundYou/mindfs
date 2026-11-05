import React from "react";
import { createPortal } from "react-dom";
import { useApkUpdateCheck } from "../../hooks/useApkUpdateCheck";
import { useTestNativeReplyAlert } from "../../hooks/useTestNativeReplyAlert";

// 从 FileTreeHeaderOverlays 拆出（硬线 500 行）：关于弹窗。
// 内容与样式逐字保留，只把「开/关」交给调用方，渲染位置不变（document.body portal）。
type FileTreeAboutDialogProps = {
  open: boolean;
  isNativeApp?: boolean;
  onClose: () => void;
};

export function FileTreeAboutDialog({ open, isNativeApp, onClose }: FileTreeAboutDialogProps) {
  const replyAlert = useTestNativeReplyAlert();
  const apkUpdate = useApkUpdateCheck();
  if (!open) return null;
  return createPortal(
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 1000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: "var(--panel-bg)", color: "var(--text-primary)", borderRadius: "12px", padding: "16px", width: "min(360px, 90vw)" }}>
        <h3 style={{ margin: "0 0 8px", fontSize: "14px" }}>关于 MindFS</h3>
        <p style={{ margin: "0 0 12px", fontSize: "12px", color: "var(--text-secondary)" }}>MindFS — AI Agent Remote Access Gateway</p>
        <p style={{ margin: "0 0 8px", fontSize: "11px", color: "var(--text-secondary)" }}>版本：{typeof window !== "undefined" && (window as any).MindFSAppInfo?.getInfo?.() ? (JSON.parse((window as any).MindFSAppInfo.getInfo()).version || "web") : "web (0.5.5-1101-g5e723830)"}</p>
        <p style={{ margin: "0 0 12px", fontSize: "11px" }}><a href="https://github.com/ChanceAroundYou/mindfs" target="_blank" rel="noreferrer" style={{ color: "var(--accent-color)" }}>github.com/ChanceAroundYou/mindfs</a></p>
        {isNativeApp ? (
          <button type="button" disabled={apkUpdate.busy} onClick={() => { void apkUpdate.run(); }} style={{ border: "1px solid var(--border-color)", background: "var(--accent-color)", color: "#fff", borderRadius: "8px", padding: "6px 12px", cursor: apkUpdate.busy ? "default" : "pointer", fontSize: "12px", marginRight: "8px", opacity: apkUpdate.busy ? 0.7 : 1 }}>
            {apkUpdate.busy ? "检查中…" : "检查更新"}
          </button>
        ) : null}
        {isNativeApp ? (
          <button type="button" onClick={() => { void replyAlert.run(); }} style={{ border: "1px solid var(--border-color)", background: "transparent", color: "var(--text-primary)", borderRadius: "8px", padding: "6px 12px", cursor: "pointer", fontSize: "12px", marginRight: "8px" }}>
            测试提醒
          </button>
        ) : null}
        {apkUpdate.message || replyAlert.message ? (
          <p style={{ margin: "10px 0 0", fontSize: "11px", lineHeight: 1.5, color: "var(--text-secondary)" }}>{apkUpdate.message || replyAlert.message}</p>
        ) : null}
        <button type="button" onClick={onClose} style={{ border: "1px solid var(--border-color)", background: "transparent", color: "var(--text-primary)", borderRadius: "8px", padding: "6px 12px", cursor: "pointer", fontSize: "12px" }}>
          关闭
        </button>
      </div>
    </div>
  , document.body);
}
