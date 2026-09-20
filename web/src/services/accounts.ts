/**
 * 账户管理 API。
 *
 * 服务端这些端点全部匿名可访问（见 docs/multi-user-prd.md §1.1）：账户只用于
 * 按账户分区，不是权限边界。所以「先验当前密码」只是防手滑的 UX 提示，不是安全校验。
 */
import { appPath } from "./base";
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
  const payload = await fetchJSON<{ users?: Account[] }>(appPath("/api/users"));
  return Array.isArray(payload?.users) ? payload.users : [];
}

export async function createAccount(username: string, password: string, role: string): Promise<Account> {
  const payload = await fetchJSON<{ user: Account }>(appPath("/api/users"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, role }),
  });
  return payload.user;
}

/**
 * 在**指定节点**上建一个同名账户。
 *
 * 账户表每台机器独立，所以「换个域名」就是换个账户系统。跨机器时本机账户 id
 * 在对方那里不存在（404），那边也就不显示任何项目——这是对的，但用户总得有个
 * 出路：去那台机器上建一个同名账户。密码由用户当场输入，不留存。
 *
 * 注意必须显式传 nodeId：`appPath` 不带 nodeId 时打的是当前页面服务器，
 * 那会变成在本机重复建号。
 */
export async function createAccountOnNode(
  nodeId: string,
  username: string,
  password: string,
  role: string,
): Promise<Account> {
  const payload = await fetchJSON<{ user: Account }>(appPath("/api/users", nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, role }),
  });
  return payload.user;
}

/** 目标节点上有没有这个用户名（跨机器认人用名字，因为两边的用户 id 必然不同）。 */
export async function nodeHasAccount(nodeId: string, username: string): Promise<boolean> {
  const payload = await fetchMaybeJSON<{ users?: Account[] }>(appPath("/api/users", nodeId));
  const list = Array.isArray(payload?.users) ? payload.users : [];
  const want = username.trim().toLowerCase();
  return list.some((u) => String(u.username || "").trim().toLowerCase() === want);
}

export async function updateAccount(
  id: string,
  patch: { username?: string; password?: string; role?: string; disabled?: boolean },
): Promise<Account> {
  const payload = await fetchJSON<{ user: Account }>(
    appPath(`/api/users/${encodeURIComponent(id)}`),
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    },
  );
  return payload.user;
}

export async function deleteAccount(id: string): Promise<void> {
  await fetchMaybeJSON(appPath(`/api/users/${encodeURIComponent(id)}`), { method: "DELETE" });
}

/** 转移主账户身份（存量数据的归属）。目标必须是启用中的管理员。 */
export async function setPrimaryAccount(id: string): Promise<void> {
  await fetchJSON(appPath(`/api/users/${encodeURIComponent(id)}/primary`), { method: "POST" });
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
  const verify = await fetch(appPath("/api/auth/login"), {
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
