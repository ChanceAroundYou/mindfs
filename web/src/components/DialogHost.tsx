import React, { useEffect, useRef, useState } from "react";
import { dialogService, type DialogRequest } from "../services/dialog";
import { useI18n } from "../i18n";

/**
 * 渲染 dialogService 的当前弹窗（确认 / 输入 / 提示）。
 *
 * 挂在 App 顶层一次即可，任何位置的 confirmDialog/promptDialog/alertDialog 都会走它。
 */

const overlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  zIndex: 1001,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: "16px",
  background: "rgba(15, 23, 42, 0.42)",
};

const panelStyle: React.CSSProperties = {
  width: "360px",
  maxWidth: "calc(100vw - 32px)",
  padding: "16px",
  borderRadius: "14px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  color: "var(--text-primary)",
  boxShadow: "0 18px 48px rgba(15, 23, 42, 0.24)",
  display: "flex",
  flexDirection: "column",
  gap: "14px",
};

const messageStyle: React.CSSProperties = {
  fontSize: "13px",
  lineHeight: 1.5,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  borderRadius: "8px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  fontSize: "13px",
  padding: "8px 10px",
  outline: "none",
};

const footerStyle: React.CSSProperties = {
  display: "flex",
  justifyContent: "flex-end",
  gap: "8px",
};

function actionButtonStyle(danger?: boolean): React.CSSProperties {
  return {
    minWidth: "72px",
    borderRadius: "8px",
    border: danger ? "none" : "1px solid var(--border-color)",
    background: danger ? "var(--danger-color, #dc2626)" : "transparent",
    color: danger ? "#fff" : "var(--text-primary)",
    fontSize: "12px",
    fontWeight: 600,
    padding: "7px 12px",
    cursor: "pointer",
  };
}

export function DialogHost(): React.ReactElement | null {
  const [request, setRequest] = useState<DialogRequest | null>(null);
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);
  const { t } = useI18n();

  useEffect(() => dialogService.subscribe((next) => {
    setRequest(next);
    setDraft(next?.kind === "prompt" ? next.defaultValue || "" : "");
  }), []);

  useEffect(() => {
    if (request?.kind === "prompt") {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [request]);

  useEffect(() => {
    if (!request) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        dialogService.dismiss();
        return;
      }
      if (event.key === "Enter" && request.kind === "confirm") {
        event.preventDefault();
        dialogService.resolve(true);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [request]);

  if (!request) {
    return null;
  }

  const cancel = () => dialogService.dismiss();
  const isPrompt = request.kind === "prompt";
  const isAlert = request.kind === "alert";
  const confirmLabel = isPrompt
    ? request.confirmLabel || t("common.save")
    : request.kind === "confirm"
      ? request.confirmLabel || t("common.confirm")
      : t("common.close");

  return (
    <div
      style={overlayStyle}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          cancel();
        }
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        style={panelStyle}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div style={messageStyle}>{request.message}</div>
        {isPrompt ? (
          <input
            ref={inputRef}
            value={draft}
            placeholder={request.placeholder || ""}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                dialogService.resolve(draft);
              }
            }}
            style={inputStyle}
          />
        ) : null}
        <div style={footerStyle}>
          {isAlert ? null : (
            <button type="button" onClick={cancel} style={actionButtonStyle(false)}>
              {isPrompt
                ? request.cancelLabel || t("common.cancel")
                : request.cancelLabel || t("common.cancel")}
            </button>
          )}
          <button
            type="button"
            onClick={() => {
              if (isPrompt) {
                dialogService.resolve(draft);
                return;
              }
              if (isAlert) {
                dialogService.resolve(true);
                return;
              }
              dialogService.resolve(true);
            }}
            style={actionButtonStyle(request.kind === "confirm" ? request.danger : false)}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
