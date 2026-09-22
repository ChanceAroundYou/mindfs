/**
 * 账户管理 API。
 *
 * 服务端这些端点全部匿名可访问（见 docs/multi-user-prd.md §1.1）：账户只用于
 * 按账户分区，不是权限边界。所以「先验当前密码」只是防手滑的 UX 提示，不是安全校验。
 */
import { pageServerPath } from "./base";
import { fetchJSON, fetchMaybeJSON } from "./api";

export type Account = {
  id: string;
  username: string;
  role: string;
  created_at?: string;
  disabled?: boolean;
  /** 主账户：它拥有迁移前的存量数据（<cfg>/ 与项目内 .mindfs/） */
  primary?: boolean;
};

export function isAdminAccount(account: Account | null | undefined): boolean {
  return account?.role === "admin";
}

export async function listAccounts(): Promise<Account[]> {
  const payload = await fetchJSON<{ users?: Account[] }>(pageServerPath("/api/users"));
  return Array.isArray(payload?.users) ? payload.users : [];
}

export async function createAccount(username: string, password: string, role: string): Promise<Account> {
  const payload = await fetchJSON<{ user: Account }>(pageServerPath("/api/users"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, role }),
  });
  return payload.user;
}

export async function updateAccount(
  id: string,
  patch: { username?: string; password?: string; role?: string; disabled?: boolean },
): Promise<Account> {
  const payload = await fetchJSON<{ user: Account }>(
    pageServerPath(`/api/users/${encodeURIComponent(id)}`),
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  return payload.user;
}

export async function deleteAccount(id: string): Promise<void> {
  await fetchMaybeJSON(pageServerPath(`/api/users/${encodeURIComponent(id)}`), { method: "DELETE" });
}

/** 转移主账户身份（存量数据的归属）。目标必须是启用中的管理员。 */
export async function setPrimaryAccount(id: string): Promise<void> {
  await fetchJSON(pageServerPath(`/api/users/${encodeURIComponent(id)}/primary`), { method: "POST" });
}

/**
 * 改密码：先用旧密码登录一次确认没打错，再改。
 * 拿不到旧密码说明是别人在改——但 API 匿名，这条只是防手滑。
 */
export async function changePassword(
  account: Account,
  currentPassword: string,
  newPassword: string,
): Promise<void> {
  const verify = await fetch(pageServerPath("/api/auth/login"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: account.username, password: currentPassword }),
  });
  if (!verify.ok) {
    throw new Error(verify.status === 401 ? "invalid_credentials" : `verify_failed_${verify.status}`);
  }
  await updateAccount(account.id, { password: newPassword });
}

/** 把服务端错误码翻成 i18n key；没收录的原样返回。 */
export function accountErrorKey(code: string): string {
  switch (code) {
    case "invalid_credentials":
      return "account.errInvalidCurrent";
    case "username_taken":
      return "account.errUsernameTaken";
    case "last_admin":
      return "account.errLastAdmin";
    case "primary_user_protected":
      return "account.errPrimaryUser";
    case "user_not_found":
      return "account.errUserNotFound";
    case "user_disabled":
      return "account.errDisabled";
    default:
      return "";
  }
}
