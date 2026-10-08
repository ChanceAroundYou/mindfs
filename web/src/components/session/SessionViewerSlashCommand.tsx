import React from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { MarkdownViewer } from "../file/MarkdownViewer";
import { copyText } from "../../services/platform/clipboard";
import { reportError } from "../../services/net/error";
import { useI18n } from "../../i18n/index";
import type { RelatedFile } from "../../services/session";
import type { SessionViewerProps } from "./sessionViewerTypes";

/**
 * 斜杠命令的结果卡片。从 `SessionViewerInner` 里抽出来：它只依赖 8 个值，
 * 其中 `t` 直接走 `useI18n()`（不往下传）。
 */
export function SessionViewerSlashCommandResult({
  slashCommandResult,
  hasVisibleTimeline,
  copiedMessageKeys,
  setCopiedMessageKeys,
  copyResetTimersRef,
  onFileClickRef,
  rootId,
  themeColor,
}: {
  slashCommandResult: SessionViewerProps["slashCommandResult"];
  hasVisibleTimeline: boolean;
  copiedMessageKeys: Record<string, true>;
  setCopiedMessageKeys: Dispatch<SetStateAction<Record<string, true>>>;
  copyResetTimersRef: MutableRefObject<Record<string, number>>;
  onFileClickRef: MutableRefObject<((file: RelatedFile & { name?: string }) => void) | undefined>;
  rootId?: string | null;
  themeColor: string;
}) {
  const { t } = useI18n();

  if (!slashCommandResult) {
    return null;
  }
  const commandLabel = `/${slashCommandResult.command || "status"}`;
  const loginNotice = slashCommandResult.loginNotice;
  const isLogin = (slashCommandResult.command || "") === "login";
  const content =
    slashCommandResult.error ||
    loginNotice?.error ||
    slashCommandResult.content ||
    (slashCommandResult.status === "running"
      ? isLogin
        ? t("session.loginWaiting")
        : t("session.statusFetching")
      : "");
  const loginCodeCopyKey = loginNotice?.loginId
    ? `login-code:${loginNotice.loginId}`
    : `login-code:${slashCommandResult.sessionKey}`;
  const loginCodeCopied = !!copiedMessageKeys[loginCodeCopyKey];
  return (
    <div
      style={{
        marginTop: hasVisibleTimeline ? "18px" : "0",
        width: "100%",
        boxSizing: "border-box",
        border: "1px solid rgba(148,163,184,0.36)",
        background: "rgba(148,163,184,0.10)",
        borderRadius: "8px",
        padding: "10px 12px",
        color: "var(--text-primary)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "8px",
          marginBottom: content ? "6px" : 0,
          fontSize: "11px",
          lineHeight: 1.4,
          color: "var(--text-secondary)",
        }}
      >
        <span style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" }}>
          {commandLabel}
        </span>
        <span>
          {slashCommandResult.status === "running"
            ? t("session.statusRunning")
            : slashCommandResult.status === "failed"
              ? t("session.statusFailed")
              : t("session.statusComplete")}
        </span>
      </div>
      {isLogin && loginNotice?.userCode ? (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "8px",
            fontSize: "13px",
            lineHeight: 1.5,
          }}
        >
          {loginNotice.verificationUrl ? (
            <a
              href={loginNotice.verificationUrl}
              target="_blank"
              rel="noreferrer"
              style={{ color: "var(--accent)", overflowWrap: "anywhere" }}
            >
              {loginNotice.verificationUrl}
            </a>
          ) : null}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              flexWrap: "wrap",
            }}
          >
            <span
              style={{
                fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
                fontSize: "20px",
                letterSpacing: "0",
                color: "var(--text-primary)",
              }}
            >
              {loginNotice.userCode}
            </span>
            <button
              type="button"
              onClick={() => {
                const userCode = loginNotice.userCode || "";
                if (!userCode) {
                  reportError("clipboard.write_failed", t("session.emptyCodeCannotCopy"));
                  return;
                }
                void copyText(userCode)
                  .then(() => {
                    setCopiedMessageKeys((prev) => ({
                      ...prev,
                      [loginCodeCopyKey]: true,
                    }));
                    if (copyResetTimersRef.current[loginCodeCopyKey]) {
                      window.clearTimeout(
                        copyResetTimersRef.current[loginCodeCopyKey],
                      );
                    }
                    copyResetTimersRef.current[loginCodeCopyKey] =
                      window.setTimeout(() => {
                        setCopiedMessageKeys((prev) => {
                          const next = { ...prev };
                          delete next[loginCodeCopyKey];
                          return next;
                        });
                        delete copyResetTimersRef.current[loginCodeCopyKey];
                      }, 1000);
                  })
                  .catch((err) => {
                    reportError(
                      "clipboard.write_failed",
                      String((err as Error)?.message || t("session.copyFailed")),
                    );
                  });
              }}
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                width: "22px",
                height: "22px",
                border: "none",
                background: "transparent",
                color: "var(--text-secondary)",
                borderRadius: "6px",
                padding: 0,
                cursor: "pointer",
              }}
              aria-label={loginCodeCopied ? t("session.codeCopied") : t("session.copyCode")}
              title={loginCodeCopied ? t("session.copied") : t("session.copyCode")}
            >
              {loginCodeCopied ? (
                <span
                  aria-hidden="true"
                  style={{ fontSize: "13px", fontWeight: 800, lineHeight: 1, color: themeColor }}
                >
                  ✓
                </span>
              ) : (
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  width="14"
                  height="14"
                  viewBox="0 0 24 24"
                  aria-hidden="true"
                >
                  <path
                    fill="currentColor"
                    d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
                  />
                  <path
                    fill="currentColor"
                    d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
                  />
                </svg>
              )}
            </button>
          </div>
          {slashCommandResult.status === "complete" ? (
            <div style={{ color: "var(--text-secondary)" }}>
              {t("session.loginComplete")}
              {loginNotice.planType ? ` · ${loginNotice.planType}` : ""}
            </div>
          ) : null}
        </div>
      ) : content ? (
        <div
          style={{
            fontSize: "13px",
            lineHeight: "1.6",
            minWidth: 0,
            overflowWrap: "anywhere",
          }}
        >
          <MarkdownViewer
            content={content}
            root={rootId || undefined}
            onFileClick={(path) => onFileClickRef.current?.({ path })}
          />
        </div>
      ) : null}
    </div>
  );
}
