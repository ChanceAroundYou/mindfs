/**
 * 单一真源：所有 API/WS/静态资源统一挂在此前缀下。
 * 编译时由 VITE_MIND_FS_BASE 决定，默认 /mindfs。
 * - /mindfs → https://host/mindfs/api/... (反代)
 * - / 或 "" → https://host/api/... (裸连/根部署)
 * 改 .env 后重编即可，不再运行时猜 location.pathname。
 */
function normalizePrefix(raw: string): string {
  const v = String(raw || "").trim().replace(/\/+$/, "");
  if (!v || v === "/") return "";
  return v.startsWith("/") ? v : `/${v}`;
}
export const DEPLOY_PREFIX: string = normalizePrefix(
  (import.meta as any).env?.VITE_MIND_FS_BASE ?? "/mindfs",
);
// 供需要 join 的地方使用，始终以 / 开头或空字符串
export function withDeployPrefix(path: string): string {
  const p = path.startsWith("/") ? path : `/${path}`;
  if (!DEPLOY_PREFIX) return p;
  return `${DEPLOY_PREFIX}${p}`;
}

// 部署前缀下的静态资源目录，例如 /mindfs/assets。（空前缀退化为 /assets）
export const ASSETS_PATH: string = withDeployPrefix("/assets");
