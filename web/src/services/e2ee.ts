// ponytail: e2ee 已移除，前端无鉴权直连。此 stub 仅保兼容，待调用方迁移后可删。
export const E2EE_HEADER = "X-MindFS-E2EE";
export const CLIENT_ID_HEADER = "X-MindFS-Client-ID";
export const PROOF_HEADER = "X-MindFS-Proof";
export const TS_HEADER = "X-MindFS-TS";
export const SECRET_STORAGE_PREFIX = "mindfs.e2ee.secret.";
export type E2EEState = { configured: boolean; required: boolean; nodeId: string; secretPresent: boolean; unlocked: boolean };
export type NativeE2EESession = { required: boolean; nodeId: string; clientId: string; transportKey: string };
export type CipherEnvelope = { nonce: string; ciphertext: string };
class StubE2EE {
  snapshot(): E2EEState { return { configured: true, required: false, nodeId: "", secretPresent: false, unlocked: false }; }
  subscribe(listener: (s: E2EEState) => void) { listener(this.snapshot()); return () => {}; }
  configure() {}
  setClientId() {}
  hasSecret() { return false; }
  getSecret() { return ""; }
  setSecret() {}
  clearSession() {}
  clearSecret() {}
  async ensureSession() { return null as any; }
  async encryptEnvelope() { return null as any; }
  async decryptEnvelope<T>(): Promise<T> { throw new Error("e2ee_removed"); }
  async encodeProtectedJSON() { throw new Error("e2ee_removed"); }
  async decodeProtectedJSON<T>(): Promise<T> { throw new Error("e2ee_removed"); }
  async encodeWSMessage(v: unknown) { return JSON.stringify(v); }
  async decodeWSMessage<T>(raw: string): Promise<T> { return JSON.parse(raw) as T; }
  async wsProofParams() { return new URLSearchParams(); }
  sessionProtectedHeaders(h?: HeadersInit) { return new Headers(h); }
  async fileProofHeaders(_m: string, _p: string, h?: HeadersInit) { return new Headers(h); }
  isProtectedJSONResponse() { return false; }
  async parseProtectedJSONResponse<T>(r: Response): Promise<T> { return r.json() as Promise<T>; }
  async protectedFetch(input: RequestInfo | URL, init?: RequestInit) { return fetch(input, init); }
  async protectedJSON<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> { const res = await fetch(input, init); return res.json() as Promise<T>; }
  isRequired() { return false; }
  currentClientId() { return ""; }
  nativeSession(): NativeE2EESession { return { required: false, nodeId: "", clientId: "", transportKey: "" }; }
  handleServerError() { return false; }
}
export const e2eeService = new StubE2EE();
