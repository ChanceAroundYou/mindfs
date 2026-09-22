/**
 * 主页面登录闸门（页面级，不是 API 鉴权）。
 *
 * 服务端只回答「有没有账户表」和「这组用户名口令对不对」；登录态由前端自己持有。
 * 这是刻意的：API 保持匿名，账户只用于前端分区——详见 docs/multi-user-prd.md §1.1，
 * 密码是装饰性的，改 URL 里的 user= 就能读到别人的数据。别把它当隔离用。
 */
import { DEPLOY_PREFIX } from "./prefix";
import { deriveLocalNodeBase } from "./nodeBase";
import { getStoredString, setStoredString, removeStoredString } from "./storage";

const USER_KEY = "mindfs.current_user";

export type AuthUser = {
  id: string;
  username: string;
  role: string;
};

export type AuthStatus = {
  /** 服务端是否配了账户表 */
  required: boolean;
};

/** 闸门必须打「发这个页面的服务器」，不能打当前选中的节点。 */
function authBaseURL(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return deriveLocalNodeBase(window.location.origin, DEPLOY_PREFIX);
}

export function currentUser(): AuthUser | null {
  const raw = String(getStoredString(USER_KEY) || "").trim();
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<AuthUser>;
    const username = String(parsed?.username || "").trim();
    if (!username) {
      return null;
    }
    return {
      id: String(parsed?.id || "").trim(),
      username,
      role: String(parsed?.role || "user").trim(),
    };
  } catch {
    return null;
  }
}

export function isAdmin(): boolean {
  return currentUser()?.role === "admin";
}

export function logout(): void {
  removeStoredString(USER_KEY);
}

/**
 * 问服务端要不要登录。网络失败 / 静态托管 / 原生壳一律返回「不需要」，
 * 宁可漏挡也不要白屏——这是页面级的门帘，不是安全边界。
 */
export async function fetchAuthStatus(): Promise<AuthStatus> {
  const base = authBaseURL();
  if (!base) {
    return { required: false };
  }
  try {
    const response = await fetch(`${base}/api/auth/status`, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      return { required: false };
    }
    const payload = (await response.json()) as { required?: boolean };
    return { required: payload.required === true };
  } catch {
    return { required: false };
  }
}

/** 凭证正确则落地账户记录，否则抛错（错误码见服务端 auth 包）。 */
export async function loginWithPassword(
  username: string,
  password: string,
): Promise<AuthUser> {
  const response = await fetch(`${authBaseURL()}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  const payload = (await response.json().catch(() => ({}))) as {
    user?: AuthUser;
    error?: string;
  };
  if (!response.ok) {
    throw new Error(String(payload.error || `login_failed_${response.status}`));
  }
  const user = payload.user;
  if (!user || !String(user.username || "").trim()) {
    throw new Error("login_invalid_response");
  }
  const normalized: AuthUser = {
    id: String(user.id || "").trim(),
    username: String(user.username).trim(),
    role: String(user.role || "user").trim(),
  };
  setStoredString(USER_KEY, JSON.stringify(normalized));
  return normalized;
}
