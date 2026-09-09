import { getApiBaseURL, getWsBaseURL, isBrowserRuntime } from "./runtime";
import { DEPLOY_PREFIX, withDeployPrefix } from "./prefix";

function ensureLeadingSlash(path: string): string {
  return path.startsWith("/") ? path : `/${path}`;
}

function joinURL(baseURL: string, path: string): string {
  return `${baseURL.replace(/\/+$/, "")}${ensureLeadingSlash(path)}`;
}

export function appPath(path: string, nodeId?: string): string {
  const pathname = ensureLeadingSlash(path);
  const apiBaseURL = getApiBaseURL(nodeId);
  if (apiBaseURL) {
    return joinURL(apiBaseURL, pathname);
  }
  // under /mindfs even when no base URL (e.g. native shell fallback or relative)
  if (DEPLOY_PREFIX) return withDeployPrefix(pathname);
  return pathname;
}

export function appURL(path: string, params?: URLSearchParams, nodeId?: string): string {
  const target = appPath(path, nodeId);
  if (!params || !params.toString()) {
    return target;
  }
  return `${target}?${params.toString()}`;
}

export function wsURL(path: string, params?: URLSearchParams, nodeId?: string): string {
  const wsBaseURL = getWsBaseURL(nodeId);
  const pathname = ensureLeadingSlash(path);
  let target = wsBaseURL ? joinURL(wsBaseURL, pathname) : (DEPLOY_PREFIX ? withDeployPrefix(pathname) : pathname);
  if (!wsBaseURL && isBrowserRuntime()) {
    const { protocol, origin } = window.location;
    if (protocol === "https:") {
      target = joinURL(`wss://${origin.slice("https://".length)}`, pathname);
    } else if (protocol === "http:") {
      target = joinURL(`ws://${origin.slice("http://".length)}`, pathname);
    }
  }
  if (!params || !params.toString()) {
    return target;
  }
  return `${target}?${params.toString()}`;
}
