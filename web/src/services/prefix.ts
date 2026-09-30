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

// relay 别名前缀：与 server rewriteRelayedFrontendContent（./assets/ -> /<prefix>-assets/）
// 同一约定生成。空前缀（根部署）退化为 /assets/。
// 单一真源：SW 拦截与前端资源缺失检测都引用它，不再硬编码 /mindfs-assets/。
export const RELAY_ASSETS_PREFIX: string = DEPLOY_PREFIX
  ? `${DEPLOY_PREFIX}-assets/`
  : "/assets/";
