import React from "react";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import { currentUser, logout, type AuthUser } from "../../services/net/authGate";
import {
  accountErrorKey,
  changePassword,
  createAccount,
  deleteAccount,
  isAdminAccount,
  listAccounts,
  setPrimaryAccount,
  type Account,
} from "../../services/accounts";

const MENU_BAR_TOP = 44; // 顶栏 36px + 6px 间距 + 2
const GUTTER = 8;
const MIN_WIDTH = 200; // 侧栏被拖到极窄时的下限，再窄就没法看了
const MAX_HEIGHT = 560;
const SIDEBAR_SELECTOR = '[data-mindfs-font-scale-region="sidebar"]';

type UseAccountPanelOptions = {
  onClose: () => void;
  /** 触发按钮；面板贴它下方展开。 */
  anchorRef?: React.RefObject<HTMLElement | null>;
};

export function useAccountPanel({ onClose, anchorRef }: UseAccountPanelOptions) {
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

  const confirmDeleteAccount = async (account: Account) => {
    const ok = await confirmDialog({
      message: t("account.confirmDelete", { name: account.username }),
      danger: true,
    });
    if (!ok) return;
    await run(async () => {
      await deleteAccount(account.id);
    });
  };

  const makePrimaryAccount = (accountId: string) => {
    void run(async () => {
      await setPrimaryAccount(accountId);
    });
  };

  const signOut = () => {
    logout();
    window.location.reload();
  };

  return {
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
    confirmDeleteAccount,
    makePrimaryAccount,
    signOut,
  };
}
