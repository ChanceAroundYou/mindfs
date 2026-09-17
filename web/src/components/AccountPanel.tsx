import React from "react";
import { useI18n } from "../i18n";
import { currentUser, logout, type AuthUser } from "../services/authGate";
import {
  accountErrorKey,
  changePassword,
  createAccount,
  deleteAccount,
  isAdminAccount,
  listAccounts,
  setPrimaryAccount,
  updateAccount,
  type Account,
} from "../services/accounts";

type AccountPanelProps = {
  onClose: () => void;
};

const OVERLAY_STYLE: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  zIndex: 90,
  background: "rgba(15, 23, 42, 0.36)",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: "24px",
};

const DIALOG_STYLE: React.CSSProperties = {
  width: "min(680px, 100%)",
  maxHeight: "88vh",
  overflow: "hidden",
  borderRadius: 12,
  background: "var(--menu-bg)",
  border: "1px solid var(--border-color)",
  boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)",
  display: "flex",
  flexDirection: "column",
};

const HEADER_STYLE: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  padding: "16px 20px",
  borderBottom: "1px solid var(--border-color)",
  flex: "0 0 auto",
};

const BODY_STYLE: React.CSSProperties = {
  flex: 1,
  minHeight: 0,
  overflow: "auto",
  padding: 20,
  display: "flex",
  flexDirection: "column",
  gap: 20,
};

const FIELD_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 8,
  padding: "9px 12px",
  fontSize: 13,
  minWidth: 0,
  width: "100%",
};

const BUTTON_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 8,
  padding: "8px 14px",
  fontSize: 13,
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const PRIMARY_BUTTON_STYLE: React.CSSProperties = {
  ...BUTTON_STYLE,
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
};

const SECTION_STYLE: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 12,
};

const SECTION_TITLE_STYLE: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  color: "var(--text-secondary)",
  letterSpacing: "0.02em",
};

const CARD_STYLE: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 10,
  padding: "14px 16px",
  borderRadius: 10,
  border: "1px solid var(--border-color)",
  background: "var(--sidebar-bg)",
};

/** 窄屏自动塌成一列，宽屏两列 */
const GRID_2_STYLE: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
  gap: 10,
};

function badgeStyle(active: boolean): React.CSSProperties {
  return {
    fontSize: 11,
    padding: "2px 8px",
    borderRadius: 6,
    border: "1px solid var(--border-color)",
    color: active ? "var(--accent-color)" : "var(--text-secondary)",
    whiteSpace: "nowrap",
  };
}

export function AccountPanel({ onClose }: AccountPanelProps): React.ReactElement {
  const { t } = useI18n();
  const [me, setMe] = React.useState<AuthUser | null>(() => currentUser());
  const [accounts, setAccounts] = React.useState<Account[]>([]);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  // 两个表单默认收起：默认视图只留「当前账户 + 账户列表」，不挤
  const [passwordOpen, setPasswordOpen] = React.useState(false);
  const [createOpen, setCreateOpen] = React.useState(false);

  // 改自己密码
  const [currentPassword, setCurrentPassword] = React.useState("");
  const [newPassword, setNewPassword] = React.useState("");
  const [confirmPassword, setConfirmPassword] = React.useState("");
  const [passwordNotice, setPasswordNotice] = React.useState("");

  // 新建账户
  const [addName, setAddName] = React.useState("");
  const [addPassword, setAddPassword] = React.useState("");
  const [addRole, setAddRole] = React.useState("user");

  const admin = isAdminAccount(me);

  const refresh = React.useCallback(async () => {
    try {
      setAccounts(await listAccounts());
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  // Esc 关闭（模态约定）
  React.useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [onClose]);

  const describeError = (e: unknown): string => {
    const code = e instanceof Error ? e.message : String(e);
    const key = accountErrorKey(code);
    return key ? t(key as never) : code;
  };

  const run = async (fn: () => Promise<void>, onDone?: () => void) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      onDone?.();
      await refresh();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const submitPassword = () => {
    if (!me) return;
    if (!currentPassword || !newPassword) {
      setError(t("account.errPasswordRequired"));
      return;
    }
    if (newPassword.length < 6) {
      setError(t("account.errPasswordTooShort"));
      return;
    }
    if (newPassword !== confirmPassword) {
      setError(t("account.errPasswordMismatch"));
      return;
    }
    setPasswordNotice("");
    void run(
      async () => {
        await changePassword(me, currentPassword, newPassword);
      },
      () => {
        setCurrentPassword("");
        setNewPassword("");
        setConfirmPassword("");
        setPasswordNotice(t("account.passwordChanged"));
      },
    );
  };

  const submitNewAccount = () => {
    const name = addName.trim();
    if (!name || !addPassword) {
      setError(t("account.errCredentialsRequired"));
      return;
    }
    if (addPassword.length < 6) {
      setError(t("account.errPasswordTooShort"));
      return;
    }
    void run(
      async () => {
        await createAccount(name, addPassword, addRole);
      },
      () => {
        setAddName("");
        setAddPassword("");
        setAddRole("user");
        setCreateOpen(false);
      },
    );
  };

  return (
    <div
      style={OVERLAY_STYLE}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <section style={DIALOG_STYLE} role="dialog" aria-modal="true">
        <header style={HEADER_STYLE}>
          <div style={{ fontWeight: 700, fontSize: 15, flex: 1 }}>{t("account.title")}</div>
          <button type="button" onClick={onClose} style={BUTTON_STYLE} aria-label={t("common.close")}>
            {t("common.close")}
          </button>
        </header>

        <div style={BODY_STYLE}>
          {/* 当前账户 */}
          {me ? (
            <div style={CARD_STYLE}>
              <div style={{ display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>
                <div
                  style={{
                    width: 38,
                    height: 38,
                    borderRadius: "50%",
                    flex: "0 0 38px",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                    background: "var(--accent-color)",
                    color: "#fff",
                    fontSize: 16,
                    fontWeight: 600,
                    textTransform: "uppercase",
                  }}
                >
                  {me.username.slice(0, 1)}
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 5, minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      fontSize: 15,
                      fontWeight: 600,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {me.username}
                  </div>
                  <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                    <span style={badgeStyle(admin)}>
                      {t(admin ? "account.roleAdmin" : "account.roleUser")}
                    </span>
                    {accounts.some((a) => a.id === me.id && a.primary) ? (
                      <span style={badgeStyle(true)}>{t("account.primary")}</span>
                    ) : null}
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <button
                  type="button"
                  style={BUTTON_STYLE}
                  onClick={() => {
                    setPasswordNotice("");
                    setPasswordOpen((v) => !v);
                  }}
                >
                  {t("account.changePassword")}
                </button>
                <button
                  type="button"
                  style={BUTTON_STYLE}
                  onClick={() => {
                    logout();
                    window.location.reload();
                  }}
                >
                  {t("account.signOut")}
                </button>
              </div>
            </div>
          ) : null}

          {error ? (
            <div
              style={{
                color: "var(--danger-color, #dc2626)",
                fontSize: 12,
                padding: "8px 12px",
                borderRadius: 8,
                border: "1px solid var(--danger-color, #dc2626)",
              }}
            >
              {error}
            </div>
          ) : null}

          {/* 改自己密码（收起状态只留按钮，避免默认视图堆表单） */}
          {me && passwordOpen ? (
            <div style={SECTION_STYLE}>
              <div style={SECTION_TITLE_STYLE}>{t("account.changePassword")}</div>
              <input
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                placeholder={t("account.currentPassword")}
                autoComplete="current-password"
                style={FIELD_STYLE}
              />
              <div style={GRID_2_STYLE}>
                <input
                  type="password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder={t("account.newPassword")}
                  autoComplete="new-password"
                  style={FIELD_STYLE}
                />
                <input
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  placeholder={t("account.confirmPassword")}
                  autoComplete="new-password"
                  style={FIELD_STYLE}
                />
              </div>
              {passwordNotice ? (
                <div style={{ color: "var(--accent-color)", fontSize: 12 }}>{passwordNotice}</div>
              ) : null}
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <button type="button" disabled={busy} onClick={submitPassword} style={PRIMARY_BUTTON_STYLE}>
                  {t("account.savePassword")}
                </button>
              </div>
            </div>
          ) : null}

          {/* 账户管理 */}
          {admin ? (
            <div style={SECTION_STYLE}>
              <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                <div style={{ ...SECTION_TITLE_STYLE, flex: 1 }}>{t("account.manage")}</div>
                <button
                  type="button"
                  style={BUTTON_STYLE}
                  onClick={() => setCreateOpen((v) => !v)}
                >
                  {createOpen ? t("common.close") : t("account.newAccount")}
                </button>
              </div>

              {createOpen ? (
                <div style={{ ...CARD_STYLE, gap: 12 }}>
                  <div style={GRID_2_STYLE}>
                    <input
                      value={addName}
                      onChange={(e) => setAddName(e.target.value)}
                      placeholder={t("account.username")}
                      spellCheck={false}
                      style={FIELD_STYLE}
                    />
                    <input
                      type="password"
                      value={addPassword}
                      onChange={(e) => setAddPassword(e.target.value)}
                      placeholder={t("account.password")}
                      autoComplete="new-password"
                      style={FIELD_STYLE}
                    />
                  </div>
                  <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                    <select
                      value={addRole}
                      onChange={(e) => setAddRole(e.target.value)}
                      style={{ ...FIELD_STYLE, cursor: "pointer", width: "auto", flex: 1 }}
                    >
                      <option value="user">{t("account.roleUser")}</option>
                      <option value="admin">{t("account.roleAdmin")}</option>
                    </select>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={submitNewAccount}
                      style={PRIMARY_BUTTON_STYLE}
                    >
                      {t("account.create")}
                    </button>
                  </div>
                </div>
              ) : null}

              <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
                {accounts.map((account) => (
                  <div key={account.id} style={CARD_STYLE}>
                    <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                      <span
                        style={{
                          flex: 1,
                          minWidth: 0,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                          fontSize: 14,
                          fontWeight: account.id === me?.id ? 600 : 400,
                          textDecoration: account.disabled ? "line-through" : "none",
                          opacity: account.disabled ? 0.6 : 1,
                        }}
                      >
                        {account.username}
                      </span>
                      <span style={badgeStyle(account.role === "admin")}>
                        {t(account.role === "admin" ? "account.roleAdmin" : "account.roleUser")}
                      </span>
                      {account.primary ? <span style={badgeStyle(true)}>{t("account.primary")}</span> : null}
                      {account.disabled ? (
                        <span style={{ ...badgeStyle(false), opacity: 0.7 }}>{t("account.disable")}</span>
                      ) : null}
                    </div>
                    <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                      {!account.primary ? (
                        <button
                          type="button"
                          disabled={busy}
                          title={t("account.makePrimaryHint")}
                          style={BUTTON_STYLE}
                          onClick={() =>
                            void run(async () => {
                              await setPrimaryAccount(account.id);
                            })
                          }
                        >
                          {t("account.makePrimary")}
                        </button>
                      ) : null}
                      <button
                        type="button"
                        disabled={busy || account.id === me?.id}
                        style={BUTTON_STYLE}
                        onClick={() =>
                          void run(async () => {
                            await updateAccount(account.id, { disabled: !account.disabled });
                          })
                        }
                      >
                        {t(account.disabled ? "account.enable" : "account.disable")}
                      </button>
                      <button
                        type="button"
                        disabled={busy || account.primary}
                        title={account.primary ? t("account.deletePrimaryHint") : ""}
                        style={BUTTON_STYLE}
                        onClick={() => {
                          if (!window.confirm(t("account.confirmDelete", { name: account.username }))) return;
                          void run(async () => {
                            await deleteAccount(account.id);
                          });
                        }}
                      >
                        {t("account.delete")}
                      </button>
                    </div>
                  </div>
                ))}
              </div>

              <div style={{ fontSize: 12, lineHeight: 1.6, color: "var(--text-secondary)" }}>
                {t("account.scopeHint")}
              </div>
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}
