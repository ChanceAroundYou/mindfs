export class APIError extends Error {
  status: number;
  payload: any;
  constructor(status: number, payload: any, fallback: string) {
    super(String(payload?.message || payload?.error || fallback));
    this.name = "APIError";
    this.status = status;
    this.payload = payload;
  }
}

// 兼容旧名：保留导出，避免一次性改动过大遗漏处崩溃
export const ProtectedAPIError = APIError;

export async function fetchJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T> {
  const response = await fetch(input, init);
  const payload = await response.json().catch(() => ({} as any));
  if (!response.ok) throw new APIError(response.status, payload, `request failed: ${response.status}`);
  return payload as T;
}

export async function fetchMaybeJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T | null> {
  const response = await fetch(input, init);
  if (response.status === 204 || response.status === 304) return null as T;
  const payload = await response.json().catch(() => ({} as any));
  if (!response.ok) throw new APIError(response.status, payload, `request failed: ${response.status}`);
  return payload as T;
}

// 旧接口兼容：protectedJSON / protectedFetch 直通 fetch（e2ee 已移除）
export async function protectedFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  return fetch(input, init);
}

export async function protectedJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T> {
  return fetchJSON<T>(input, init);
}

export function protectedAPIReady(): boolean {
  return true;
}
