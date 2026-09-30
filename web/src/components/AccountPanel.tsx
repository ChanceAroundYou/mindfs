import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { Select } from "./Select";
import { currentUser, logout, type AuthUser } from "../services/authGate";
import { confirmDialog } from "../services/dialog";
import {
  accountErrorKey,
  changePassword,
  createAccount,
  deleteAccount,
  isAdminAccount,
  listAccounts,
  setPrimaryAccount,
  type Account,
} from "../services/accounts";

type AccountPanelProps = {
  onClose: () => void;
  /** 触发按钮；面板贴它下方展开。 */
  anchorRef?: React.RefObject<HTMLElement | null>;
};

// 锚定在文件树顶栏下方的下拉面板（沿用 NodeManagerPanel 的观感）。
// 必须 portal 到 body：侧栏 <aside> 带 transform / will-change（移动端抽屉），
// 会把 position:fixed 的包含块锁死在侧栏内 —— 面板被压成 ~220px 且被裁切。
// portal 之后包含块回到视口，宽度由自己说了算。
// 宽度不越出左侧栏：面板右缘对齐侧栏右缘（侧栏可拖拽，所以按实测算而不是写死 260）。
const MENU_BAR_TOP = 44; // 顶栏 36px + 6px 间距 + 2
const GUTTER = 8;
const MIN_WIDTH = 200; // 侧栏被拖到极窄时的下限，再窄就没法看了
const MAX_HEIGHT = 560;
const SIDEBAR_SELECTOR = '[data-mindfs-font-scale-region="sidebar"]';

const FIELD_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 8,
  padding: "6px 8px",
  fontSize: 12,
  minWidth: 0,
  width: "100%",
  boxSizing: "border-box",
};

const BUTTON_STYLE: React.CSSProperties = {
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
  ...BUTTON_STYLE,
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
};

/** 删除：红色垃圾桶图标按钮 */
const TRASH_BUTTON_STYLE: React.CSSProperties = {
  border: "none",
  background: "transparent",
  color: "var(--danger-color, #dc2626)",
  padding: "4px",
  borderRadius: 6,
  cursor: "pointer",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
};

function TrashIcon(): React.ReactElement {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M3 6h18" />
      <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}

const SECTION_STYLE: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 8,
  paddingTop: 10,
  borderTop: "1px solid var(--border-color)",
};

const SECTION_TITLE_STYLE: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: "var(--text-secondary)",
};

const ROW_STYLE: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  minWidth: 0,
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

export function AccountPanel({ onClose, anchorRef }: AccountPanelProps): React.ReactElement | null {
  const { t } = useI18n();
  const rootRef = React.useRef<HTMLDivElement | null>(null);
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

  const [box, setBox] = React.useState<{ top: number; left: number; width: number; maxHeight: number } | null>(
    null,
  );

  const admin = isAdminAccount(me);

  // 贴住顶栏、右缘对齐左侧栏右缘（不越出侧栏），并夹在视口里
  React.useLayoutEffect(() => {
    const place = () => {
      const sidebar =
        anchorRef?.current?.closest(SIDEBAR_SELECTOR) ?? document.querySelector(SIDEBAR_SELECTOR);
      const rect = sidebar?.getBoundingClientRect();
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const available = rect ? rect.width - GUTTER * 2 : vw - GUTTER * 2;
      const width = Math.max(MIN_WIDTH, Math.min(available, vw - GUTTER * 2));
      const left = Math.max(GUTTER, Math.min(rect ? rect.left + GUTTER : GUTTER, vw - width - GUTTER));
      const top = Math.max(GUTTER, Math.min(MENU_BAR_TOP, vh - GUTTER));
      setBox({ top, left, width, maxHeight: Math.max(160, Math.min(MAX_HEIGHT, vh - top - GUTTER)) });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [anchorRef]);

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

  // 点外部 / Esc 关闭（沿用 NodeManagerPanel 的约定）
  React.useEffect(() => {
    const onPointerDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (rootRef.current?.contains(target)) return;
      if (anchorRef?.current?.contains(target)) return; // 锚点自己走 onClick，别抢
      onClose();
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [onClose, anchorRef]);

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

  if (!box) return null;

  const panel = (
    <div
      ref={rootRef}
      role="dialog"
      aria-label={t("account.title")}
      style={{
        position: "fixed",
        top: box.top,
        left: box.left,
        width: box.width,
        maxHeight: box.maxHeight,
        overflow: "auto",
        boxSizing: "border-box",
        zIndex: 60,
        padding: 12,
        borderRadius: 12,
        border: "1px solid var(--border-color)",
        background: "var(--menu-bg)",
        boxShadow: "0 12px 30px rgba(15,23,42,0.14)",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div style={{ ...ROW_STYLE, flexWrap: "wrap" }}>
        <span style={{ fontSize: 12, fontWeight: 700 }}>{t("account.title")}</span>
        {/* 关掉面板靠点外部 / Esc；右上角让给改密码与退出 */}
        <div style={{ display: "flex", gap: 8, marginLeft: "auto", flexWrap: "wrap" }}>
          {me ? (
            <>
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
            </>
          ) : null}
        </div>
      </div>

      {me ? (
        <div style={{ ...ROW_STYLE, flexWrap: "wrap" }}>
          <span style={{ fontSize: 12, fontWeight: 600, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {me.username}
          </span>
          <span style={badgeStyle(admin)}>{t(admin ? "account.roleAdmin" : "account.roleUser")}</span>
          {accounts.some((a) => a.id === me.id && a.primary) ? (
            <span style={badgeStyle(true)}>{t("account.primary")}</span>
          ) : null}
        </div>
      ) : null}

      {error ? <div style={{ color: "var(--danger-color, #dc2626)", fontSize: 11 }}>{error}</div> : null}

      {/* 改自己密码（默认收起，避免默认视图堆表单） */}
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
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
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
            <div style={{ color: "var(--accent-color)", fontSize: 11 }}>{passwordNotice}</div>
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
          <div style={{ ...ROW_STYLE, flexWrap: "wrap" }}>
            <span style={{ ...SECTION_TITLE_STYLE, flex: 1 }}>{t("account.manage")}</span>
            <button type="button" style={BUTTON_STYLE} onClick={() => setCreateOpen((v) => !v)}>
              {createOpen ? t("common.close") : t("account.newAccount")}
            </button>
          </div>

          {createOpen ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
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
              <div style={ROW_STYLE}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <Select
                    value={addRole}
                    onChange={setAddRole}
                    options={[
                      { value: "user", label: t("account.roleUser") },
                      { value: "admin", label: t("account.roleAdmin") },
                    ]}
                  />
                </div>
                <button type="button" disabled={busy} onClick={submitNewAccount} style={PRIMARY_BUTTON_STYLE}>
                  {t("account.create")}
                </button>
              </div>
            </div>
          ) : null}

          {accounts.map((account) => (
            <div
              key={account.id}
              style={{
                display: "flex",
                flexDirection: "column",
                gap: 6,
                paddingTop: 6,
                borderTop: "1px solid var(--border-color)",
              }}
            >
              <div style={{ ...ROW_STYLE, flexWrap: "wrap" }}>
                <span
                  style={{
                    fontSize: 12,
                    fontWeight: account.id === me?.id ? 600 : 400,
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
                {account.disabled ? (
                  <span style={{ ...badgeStyle(false), opacity: 0.7 }}>{t("account.disable")}</span>
                ) : null}
                <button
                  type="button"
                  disabled={busy || account.primary}
                  aria-label={t("account.delete")}
                  title={account.primary ? t("account.deletePrimaryHint") : t("account.delete")}
                  style={{
                    ...TRASH_BUTTON_STYLE,
                    marginLeft: "auto",
                    opacity: busy || account.primary ? 0.45 : 1,
                  }}
                  onClick={() => {
                    void confirmDialog({
                      message: t("account.confirmDelete", { name: account.username }),
                      danger: true,
                    }).then((ok) => {
                      if (!ok) return;
                      return run(async () => {
                        await deleteAccount(account.id);
                      });
                    });
                  }}
                >
                  <TrashIcon />
                </button>
              </div>
              <div style={{ ...ROW_STYLE, flexWrap: "wrap" }}>
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
              </div>
            </div>
          ))}

          <div style={{ fontSize: 11, lineHeight: 1.5, color: "var(--text-secondary)" }}>
            {t("account.scopeHint")}
          </div>
        </div>
      ) : null}
    </div>
  );

  return createPortal(panel, document.body);
}
