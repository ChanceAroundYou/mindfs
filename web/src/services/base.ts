import { getApiBaseURL, getWsBaseURL, isBrowserRuntime } from "./runtime";

function ensureLeadingSlash(path: string): string {
  return path.startsWith("/") ? path : `/${path}`;
}

function joinURL(baseURL: string, path: string): string {
  return `${baseURL.replace(/\/+$/, "")}${ensureLeadingSlash(path)}`;
}

export function appPath(path: string): string {
  const pathname = ensureLeadingSlash(path);
  // In Capacitor runtime, relative paths won't resolve to the MindFS backend.
  // Return a full URL so fetch(appPath(...)) works the same as fetch(appURL(...)).
  const apiBaseURL = getApiBaseURL();
  if (apiBaseURL) {
    return joinURL(apiBaseURL, pathname);
  }
  return pathname;
}

export function appURL(path: string, params?: URLSearchParams): string {
  const target = appPath(path);
  if (!params || !params.toString()) {
    return target;
  }
  return `${target}?${params.toString()}`;
}

export function wsURL(path: string, params?: URLSearchParams): string {
  const wsBaseURL = getWsBaseURL();
  const pathname = ensureLeadingSlash(path);
  let target = wsBaseURL ? joinURL(wsBaseURL, pathname) : pathname;
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
