import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { Select } from "../common/Select";
import { logout } from "../../services/net/authGate";
import { confirmDialog } from "../../services/dialog";
import { deleteAccount, setPrimaryAccount } from "../../services/accounts";
import { useAccountPanel } from "./useAccountPanel";

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
  const {
    rootRef,
    me,
    accounts,
    error,
    busy,
    admin,
    passwordOpen,
    createOpen,
    currentPassword,
    newPassword,
    confirmPassword,
    passwordNotice,
    addName,
    addPassword,
    addRole,
    box,
    setPasswordOpen,
    setCurrentPassword,
    setNewPassword,
    setConfirmPassword,
    setPasswordNotice,
    setAddName,
    setAddPassword,
    setAddRole,
    setCreateOpen,
    run,
    submitPassword,
    submitNewAccount,
  } = useAccountPanel({ onClose, anchorRef });

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
