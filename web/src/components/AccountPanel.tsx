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

const PANEL_STYLE: React.CSSProperties = {
  position: "absolute",
  top: "calc(100% + 6px)",
  left: "8px",
  right: "8px",
  zIndex: 35,
  padding: "12px",
  borderRadius: "12px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15,23,42,0.14)",
  display: "flex",
  flexDirection: "column",
  gap: 8,
  maxHeight: 420,
  overflow: "auto",
};

const INPUT_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 8,
  padding: "6px 8px",
  fontSize: 12,
  minWidth: 0,
  flex: 1,
};

const SMALL_BUTTON_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const PRIMARY_BUTTON_STYLE: React.CSSProperties = {
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const SECTION_STYLE: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 6,
  paddingTop: 8,
  borderTop: "1px solid var(--border-color)",
};

function badgeStyle(active: boolean): React.CSSProperties {
  return {
    fontSize: 10,
    padding: "1px 6px",
    borderRadius: 6,
    border: "1px solid var(--border-color)",
    color: active ? "var(--accent-color)" : "var(--text-secondary)",
    whiteSpace: "nowrap",
  };
}

export function AccountPanel({ onClose }: AccountPanelProps): React.ReactElement {
  const { t } = useI18n();
  const rootRef = React.useRef<HTMLDivElement | null>(null);
  const [me, setMe] = React.useState<AuthUser | null>(() => currentUser());
  const [accounts, setAccounts] = React.useState<Account[]>([]);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

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

  // 外部点击关闭（沿用 NodeManagerPanel 的约定）
  React.useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
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
      },
    );
  };

  return (
    <div ref={rootRef} style={PANEL_STYLE}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ fontWeight: 700, fontSize: 12, flex: 1 }}>{t("account.title")}</div>
        <button type="button" onClick={onClose} style={SMALL_BUTTON_STYLE}>
          {t("common.close")}
        </button>
      </div>

      {me ? (
        <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12 }}>
          <span style={{ fontWeight: 600 }}>{me.username}</span>
          <span style={badgeStyle(admin)}>
            {t(admin ? "account.roleAdmin" : "account.roleUser")}
          </span>
          <span style={{ flex: 1 }} />
          <button
            type="button"
            style={SMALL_BUTTON_STYLE}
            onClick={() => {
              logout();
              window.location.reload();
            }}
          >
            {t("account.signOut")}
          </button>
        </div>
      ) : null}

      {error ? <div style={{ color: "var(--danger-color, #dc2626)", fontSize: 11 }}>{error}</div> : null}

      {me ? (
        <div style={SECTION_STYLE}>
          <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>{t("account.changePassword")}</div>
          <div style={{ display: "flex", gap: 6 }}>
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              placeholder={t("account.currentPassword")}
              autoComplete="current-password"
              style={INPUT_STYLE}
            />
          </div>
          <div style={{ display: "flex", gap: 6 }}>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder={t("account.newPassword")}
              autoComplete="new-password"
              style={INPUT_STYLE}
            />
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder={t("account.confirmPassword")}
              autoComplete="new-password"
              style={INPUT_STYLE}
            />
          </div>
          {passwordNotice ? (
            <div style={{ color: "var(--accent-color)", fontSize: 11 }}>{passwordNotice}</div>
          ) : null}
          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <button type="button" disabled={busy} onClick={submitPassword} style={PRIMARY_BUTTON_STYLE}>
              {t("account.savePassword")}
            </button>
          </div>
        </div>
      ) : null}

      {admin ? (
        <div style={SECTION_STYLE}>
          <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>{t("account.manage")}</div>
          {accounts.map((account) => (
            <div key={account.id} style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12 }}>
              <span
                style={{
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
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
              {!account.primary ? (
                <button
                  type="button"
                  disabled={busy}
                  title={t("account.makePrimaryHint")}
                  style={SMALL_BUTTON_STYLE}
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
                style={SMALL_BUTTON_STYLE}
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
                style={SMALL_BUTTON_STYLE}
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
          ))}

          <div style={{ display: "flex", gap: 6, marginTop: 4 }}>
            <input
              value={addName}
              onChange={(e) => setAddName(e.target.value)}
              placeholder={t("account.username")}
              spellCheck={false}
              style={INPUT_STYLE}
            />
            <input
              type="password"
              value={addPassword}
              onChange={(e) => setAddPassword(e.target.value)}
              placeholder={t("account.password")}
              autoComplete="new-password"
              style={INPUT_STYLE}
            />
            <select
              value={addRole}
              onChange={(e) => setAddRole(e.target.value)}
              style={{ ...SMALL_BUTTON_STYLE, cursor: "pointer" }}
            >
              <option value="user">{t("account.roleUser")}</option>
              <option value="admin">{t("account.roleAdmin")}</option>
            </select>
            <button type="button" disabled={busy} onClick={submitNewAccount} style={PRIMARY_BUTTON_STYLE}>
              {t("account.create")}
            </button>
          </div>
          <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>{t("account.scopeHint")}</div>
        </div>
      ) : null}
    </div>
  );
}
