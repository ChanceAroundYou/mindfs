import type { Dispatch, RefObject, SetStateAction } from "react";

// 从 App.tsx 抽出的纯函数（机械搬移，函数体逐字未改）。
import { reportError } from "../services/net/error";
import { copyText } from "../platform/clipboard";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./appSession";
import { useI18n } from "../i18n";

export interface RenderRootSlashCommandResultParams {
  copiedSlashCommandKeys: Record<string, true>;
  copyText: typeof copyText;
  reportError: typeof reportError;
  setCopiedSlashCommandKeys: Dispatch<SetStateAction<Record<string, true>>>;
  slashCopyResetTimersRef: RefObject<Record<string, number>>;
  t: ReturnType<typeof useI18n>["t"];
}

export function renderRootSlashCommandResult(params: RenderRootSlashCommandResultParams) {
  const copiedSlashCommandKeys = params.copiedSlashCommandKeys;
  const copyText = params.copyText;
  const reportError = params.reportError;
  const setCopiedSlashCommandKeys = params.setCopiedSlashCommandKeys;
  const slashCopyResetTimersRef = params.slashCopyResetTimersRef;
  const t = params.t;

  return (
    (result: SlashCommandResult | null) => {
        if (!result || !result.sessionKey.startsWith("transient-")) {
          return null;
        }
        const commandLabel = `/${result.command || "status"}`;
        const loginNotice = result.loginNotice;
        const isLogin = (result.command || "") === "login";
        const fallback =
          result.status === "running"
            ? isLogin
              ? t("session.loginWaiting")
              : t("session.statusFetching")
            : "";
        const content = result.error || loginNotice?.error || result.content || fallback;
        const loginCodeCopyKey = loginNotice?.loginId
          ? `login-code:${loginNotice.loginId}`
          : `login-code:${result.sessionKey}`;
        const loginCodeCopied = !!copiedSlashCommandKeys[loginCodeCopyKey];
        return (
          <div
            style={{
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
                marginBottom: content || loginNotice?.userCode ? "6px" : 0,
                fontSize: "11px",
                lineHeight: 1.4,
                color: "var(--text-secondary)",
              }}
            >
              <span style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" }}>
                {commandLabel}
              </span>
              <span>{result.status === "running" ? t("session.statusRunning") : result.status === "failed" ? t("session.statusFailed") : t("session.statusComplete")}</span>
            </div>
            {isLogin && loginNotice?.userCode ? (
              <div style={{ display: "flex", flexDirection: "column", gap: "8px", fontSize: "13px", lineHeight: 1.5 }}>
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
                <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                  <span
                    style={{
                      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
                      fontSize: "20px",
                      letterSpacing: "0",
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
                          setCopiedSlashCommandKeys((prev) => ({
                            ...prev,
                            [loginCodeCopyKey]: true,
                          }));
                          if (slashCopyResetTimersRef.current[loginCodeCopyKey]) {
                            window.clearTimeout(
                              slashCopyResetTimersRef.current[loginCodeCopyKey],
                            );
                          }
                          slashCopyResetTimersRef.current[loginCodeCopyKey] =
                            window.setTimeout(() => {
                              setCopiedSlashCommandKeys((prev) => {
                                const next = { ...prev };
                                delete next[loginCodeCopyKey];
                                return next;
                              });
                              delete slashCopyResetTimersRef.current[
                                loginCodeCopyKey
                              ];
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
                        style={{ fontSize: "13px", fontWeight: 800, lineHeight: 1 }}
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
                {result.status === "complete" ? (
                  <div style={{ color: "var(--text-secondary)" }}>
                    {t("session.loginComplete")}{loginNotice.planType ? ` · ${loginNotice.planType}` : ""}
                  </div>
                ) : null}
              </div>
            ) : content ? (
              <div
                style={{
                  fontSize: "13px",
                  lineHeight: "1.6",
                  whiteSpace: "pre-wrap",
                  overflowWrap: "anywhere",
                }}
              >
                {content}
              </div>
            ) : null}
          </div>
        );
      }
  );
}
