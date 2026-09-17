/**
 * 主页面登录闸门（页面级，不是 API 鉴权）。
 *
 * 服务端只回答两件事：这个浏览器要不要登录、这个 token 还算不算数。
 * 所有 REST/WS 依旧匿名可访问——REST 会保持这样，别在这里加拦截。
 */
import { DEPLOY_PREFIX } from "./prefix";
import { deriveLocalNodeBase } from "./nodeBase";
import { getStoredString, setStoredString, removeStoredString } from "./storage";

const TOKEN_KEY = "mindfs.login.token";

/** 闸门必须打「发这个页面的服务器」，不能打当前选中的节点。 */
function authBaseURL(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return deriveLocalNodeBase(window.location.origin, DEPLOY_PREFIX);
}

type StatusPayload = {
  required?: boolean;
  authed?: boolean;
};

export type AuthStatus = {
  /** 服务端是否启用了登录闸门 */
  required: boolean;
  /** 本地 token 是否仍然有效 */
  authed: boolean;
};

export function readAuthToken(): string {
  return String(getStoredString(TOKEN_KEY) || "").trim();
}

export function clearAuthToken(): void {
  removeStoredString(TOKEN_KEY);
}

/**
 * 问服务端要不要登录。网络失败 / 静态托管 / 原生壳一律返回「不需要」，
 * 宁可漏挡也不要白屏——这是页面级的门帘，不是安全边界。
 */
export async function fetchAuthStatus(): Promise<AuthStatus> {
  const base = authBaseURL();
  if (!base) {
    return { required: false, authed: true };
  }
  const token = readAuthToken();
  const query = token ? `?token=${encodeURIComponent(token)}` : "";
  try {
    const response = await fetch(`${base}/api/auth/status${query}`, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      return { required: false, authed: true };
    }
    const payload = (await response.json()) as StatusPayload;
    if (payload.required !== true) {
      return { required: false, authed: true };
    }
    return { required: true, authed: payload.authed === true };
  } catch {
    return { required: false, authed: true };
  }
}

/** 密码正确则落地 token，否则抛错（错误码 invalid_password）。 */
export async function loginWithPassword(password: string): Promise<void> {
  const response = await fetch(`${authBaseURL()}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password }),
  });
  const payload = (await response.json().catch(() => ({}))) as {
    token?: string;
    error?: string;
  };
  if (!response.ok) {
    throw new Error(String(payload.error || `login_failed_${response.status}`));
  }
  const token = String(payload.token || "").trim();
  if (!token) {
    throw new Error("login_invalid_response");
  }
  clearAuthToken();
  setStoredString(TOKEN_KEY, token);
}
