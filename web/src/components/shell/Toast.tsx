import React from "react";
import type { AppError } from "../../shared/errorTypes";
import { useI18n } from "../../i18n/index";
import { useToastErrors } from "../../hooks/useToastErrors";

type ToastItem = {
  id: string;
  error: AppError;
  expiresAt: number;
};

export function ToastContainer(): React.ReactElement {
  const { toasts, removeToast, handleRetry } = useToastErrors();

  if (toasts.length === 0) return <></>;

  return (
    <div
      style={{
        position: "fixed",
        bottom: "24px",
        left: "50%",
        transform: "translateX(-50%)",
        display: "flex",
        flexDirection: "column",
        gap: "8px",
        zIndex: 1000,
        maxWidth: "640px",
        width: "100%",
        padding: "0 16px",
      }}
    >
      {toasts.map((toast) => (
        <Toast
          key={toast.id}
          error={toast.error}
          onClose={() => removeToast(toast.id)}
          onRetry={toast.error.recoverable ? () => handleRetry(toast) : undefined}
        />
      ))}
    </div>
  );
}

type ToastProps = {
  error: AppError;
  onClose: () => void;
  onRetry?: () => void;
};

function Toast({ error, onClose, onRetry }: ToastProps): React.ReactElement {
  const { t } = useI18n();
  // CUSTOM(G-AE): 兜底色用主题变量，上游写死的蓝不随节点换色
  const bgColor =
    error.severity === "error"
      ? "rgba(239, 68, 68, 0.95)"
      : error.severity === "warning"
      ? "rgba(245, 158, 11, 0.95)"
      : "var(--accent-color)";

  const message = error.usesDefaultMessage && error.messageKey ? t(error.messageKey) : error.message;

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "12px",
        padding: "12px 16px",
        background: bgColor,
        borderRadius: "10px",
        boxShadow: "0 4px 12px rgba(0,0,0,0.15)",
        color: "#fff",
        animation: "toastSlideIn 0.2s ease-out",
      }}
    >
      <style>
        {`
          @keyframes toastSlideIn {
            from {
              opacity: 0;
              transform: translateY(20px);
            }
            to {
              opacity: 1;
              transform: translateY(0);
            }
          }
        `}
      </style>

      {/* Icon */}
      <span style={{ fontSize: "18px" }}>
        {error.severity === "error" ? "❌" : error.severity === "warning" ? "⚠️" : "ℹ️"}
      </span>

      {/* Message */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            fontSize: "13px",
            fontWeight: 500,
            whiteSpace: "pre-wrap",
            overflowWrap: "anywhere",
            lineHeight: 1.45,
          }}
          title={message}
        >
          {message}
        </div>
        {/* 错误码那一行（`file.write_failed` 之类）2026-10-07 删掉：它从来不是
            给用户看的信息 —— 用户看到「写入文件失败」只会去查磁盘，而看板任务
            动作失败时挂上这个码更是纯粹的误导。要排查看日志。 */}
      </div>

      {/* Actions */}
      <div style={{ display: "flex", gap: "8px" }}>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            style={{
              padding: "4px 10px",
              borderRadius: "6px",
              border: "1px solid rgba(255,255,255,0.3)",
              background: "rgba(255,255,255,0.1)",
              color: "#fff",
              fontSize: "12px",
              cursor: "pointer",
            }}
          >
            {t("common.retry")}
          </button>
        )}
        <button
          type="button"
          onClick={onClose}
          style={{
            padding: "4px 8px",
            borderRadius: "6px",
            border: "none",
            background: "transparent",
            color: "#fff",
            fontSize: "14px",
            cursor: "pointer",
            opacity: 0.8,
          }}
        >
          ✕
        </button>
      </div>
    </div>
  );
}
