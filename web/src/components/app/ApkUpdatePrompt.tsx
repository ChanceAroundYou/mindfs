import React from "react";
import { createPortal } from "react-dom";
import { useApkAutoUpdate } from "../../hooks/useApkAutoUpdate";

// 自动检查到新版本时的全局弹窗。挂到 document.body，避免被侧栏/主区的
// 层叠上下文裁切（关于弹窗踩过同一个坑）。
export function ApkUpdatePrompt() {
  const { release, message, busy, apply, later } = useApkAutoUpdate();
  if (!release) return null;
  return createPortal(
    <div
      onClick={later}
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 1001 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ background: "var(--panel-bg)", color: "var(--text-primary)", borderRadius: "12px", padding: "16px", width: "min(360px, 90vw)", boxShadow: "0 18px 36px rgba(15, 23, 42, 0.24)" }}
      >
        <h3 style={{ margin: "0 0 8px", fontSize: "14px" }}>发现新版本</h3>
        <p style={{ margin: "0 0 12px", fontSize: "12px", color: "var(--text-secondary)", whiteSpace: "pre-wrap", lineHeight: 1.5 }}>
          {message || `当前有新版本 ${release.tag} 可安装。`}
        </p>
        <button
          type="button"
          disabled={busy}
          onClick={() => { void apply(); }}
          style={{ border: "1px solid var(--border-color)", background: "var(--accent-color)", color: "#fff", borderRadius: "8px", padding: "6px 12px", cursor: busy ? "default" : "pointer", fontSize: "12px", marginRight: "8px", opacity: busy ? 0.7 : 1 }}
        >
          {busy ? "准备中…" : "立即更新"}
        </button>
        <button
          type="button"
          onClick={later}
          style={{ border: "1px solid var(--border-color)", background: "transparent", color: "var(--text-primary)", borderRadius: "8px", padding: "6px 12px", cursor: "pointer", fontSize: "12px" }}
        >
          稍后
        </button>
      </div>
    </div>,
    document.body,
  );
}
