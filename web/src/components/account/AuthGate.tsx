import React from "react";
import { useI18n } from "../../i18n/index";
import { useAuthGate } from "./useAuthGate";

/**
 * 主页面登录闸门：没登录就只渲染一张登录卡片，App 完全不被挂载。
 * 这是页面级的门帘（服务端 API 依旧公开），不是安全边界——见 services/authGate.ts。
 */
export function AuthGate({ children }: { children: React.ReactNode }) {
  const { t } = useI18n();
  const { state, username, setUsername, password, setPassword, error, setError, busy, submit } = useAuthGate(t);

  if (state === "open") {
    return <>{children}</>;
  }

  // 校验中先空屏：绝不能先闪一下主界面再盖上去
  if (state === "checking") {
    return <div style={{ position: "fixed", inset: 0, background: "var(--mindfs-launcher-bg)" }} />;
  }

  const fieldStyle: React.CSSProperties = {
    width: "100%",
    borderRadius: "14px",
    border: "1px solid var(--mindfs-launcher-border-strong)",
    background: "var(--mindfs-launcher-input-bg)",
    color: "var(--mindfs-launcher-text)",
    padding: "14px 16px",
    fontSize: "14px",
    outline: "none",
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--mindfs-launcher-bg)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "24px",
      }}
    >
      <div
        style={{
          width: "min(420px, 100%)",
          background: "var(--mindfs-launcher-surface-strong)",
          border: "1px solid var(--mindfs-launcher-border)",
          borderRadius: "20px",
          padding: "28px",
          boxShadow: "var(--mindfs-launcher-shadow)",
          display: "flex",
          flexDirection: "column",
          gap: "16px",
        }}
      >
        <div style={{ fontSize: "20px", fontWeight: 700, color: "var(--mindfs-launcher-text)" }}>
          {t("login.title")}
        </div>
        <div style={{ fontSize: "13px", color: "var(--mindfs-launcher-muted)", marginTop: "-10px" }}>
          {t("login.subtitle")}
        </div>
        <input
          type="text"
          value={username}
          onChange={(event) => {
            setUsername(event.target.value);
            if (error) {
              setError("");
            }
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !busy) {
              void submit();
            }
          }}
          placeholder={t("login.usernamePlaceholder")}
          autoFocus
          autoComplete="username"
          spellCheck={false}
          style={fieldStyle}
        />
        <input
          type="password"
          value={password}
          onChange={(event) => {
            setPassword(event.target.value);
            if (error) {
              setError("");
            }
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !busy) {
              void submit();
            }
          }}
          placeholder={t("login.passwordPlaceholder")}
          autoComplete="current-password"
          spellCheck={false}
          style={fieldStyle}
        />
        {error ? (
          <div style={{ color: "var(--mindfs-launcher-error-text)", fontSize: "13px" }}>{error}</div>
        ) : null}
        <button
          type="button"
          onClick={() => void submit()}
          disabled={busy}
          style={{
            border: "none",
            borderRadius: "999px",
            background: busy ? "var(--mindfs-launcher-border-strong)" : "var(--mindfs-launcher-accent)",
            color: "#fff",
            padding: "12px 18px",
            cursor: busy ? "not-allowed" : "pointer",
            fontWeight: 600,
            fontSize: "14px",
          }}
        >
          {busy ? t("login.verifying") : t("login.submit")}
        </button>
        <div style={{ fontSize: "12px", color: "var(--mindfs-launcher-muted)" }}>
          {t("login.hint")}
        </div>
      </div>
    </div>
  );
}
