// 边控端首次配对表单的纯逻辑：校验与归一。
//
// 规则必须与服务端一致（`controlsync.Pair` → `ValidateSyncToken` + `parsePrimaryURL`）：
// 这里漏一条，用户会填完点提交才看到 400；这里多一条，服务端能过的输入会被前端挡住。
//
// 视图层（components）只能读这里，不能直接 import services。
export type PairingDraft = {
  primaryURL: string;
  token: string;
  caFile: string;
};

export type PairingDraftIssue = "url" | "urlScheme" | "token";

export const SYNC_TOKEN_MIN_BYTES = 16;

export function normalizePairingDraft(draft: PairingDraft): PairingDraft {
  return {
    primaryURL: draft.primaryURL.trim(),
    token: draft.token.trim(),
    caFile: draft.caFile.trim(),
  };
}

/**
 * 返回第一个不满足的字段；null = 可以提交。
 *
 * 顺序按表单从上到下：先地址、后密钥 —— 指向第一个出错的输入框。
 */
export function pairingDraftIssue(draft: PairingDraft): PairingDraftIssue | null {
  const { primaryURL, token } = normalizePairingDraft(draft);
  if (!primaryURL) return "url";
  // 没有 `://` 就不算地址：`new URL("home.example:7331")` 会把 `home.example:`
  // 解析成 scheme，于是用户少打一个协议头得到的提示会是「只接受 https」——
  // 那句话对着一个没写协议的输入只会让人困惑。
  if (!primaryURL.includes("://")) return "url";
  let parsed: URL;
  try {
    parsed = new URL(primaryURL);
  } catch {
    return "url";
  }
  // 同步通道搬的是整份控制面（含口令 hash），明文 http 一律不收：
  // 服务端硬性拒绝，前端也提前说明，别让用户以为是网络问题。
  if (parsed.protocol !== "https:" || !parsed.hostname) return "urlScheme";
  if (!isSyncTokenShape(token)) return "token";
  return null;
}

/** 与服务端 ValidateSyncToken 同口径：≥16 字节、无空白。 */
export function isSyncTokenShape(token: string): boolean {
  const value = token.trim();
  if (value.length < SYNC_TOKEN_MIN_BYTES) return false;
  return !/\s/.test(value);
}

/**
 * 提交载荷：caFile 留空就不发这个键。
 *
 * 服务端把空串当「没有自定义 CA」处理，但少一个键更贴近「用户没提这件事」，
 * 也让请求体在日志里更容易读。
 */
export function toPairingRequest(draft: PairingDraft): {
  primary_url: string;
  token: string;
  ca_file?: string;
} {
  const { primaryURL, token, caFile } = normalizePairingDraft(draft);
  return caFile ? { primary_url: primaryURL, token, ca_file: caFile } : { primary_url: primaryURL, token };
}
