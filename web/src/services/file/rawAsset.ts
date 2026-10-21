import { accountSegmentFor, appBasePath } from "../net/base";
import { getRootNodeId } from "../net/rootNode";

/**
 * 路径式原始资源 URL：/api/file/raw/{account}/{root}/{path...}
 *
 * 为什么不用现有的 /api/file?...&raw=1：
 *   - 网页里的相对 URL（style.css、img/x.png）在浏览器里解析时**会丢掉查询串**，
 *     路径式才能让相对资源落到同目录的兄弟文件上；
 *   - 账户必须编码在路径里：相对资源的请求同样不带 user=，
 *     账户在路径里才保得住「按账户分区」（非主账户的项目不会掉回主账户）。
 *
 * 拿不到账户段时返回空串 —— 调用方据此提示「预览不出来，请下载」，
 * 不要拼一个缺段的 URL 出去（会 404，还会被误读成「文件不存在」）。
 */
export function buildRawAssetURL(params: {
  rootId: string;
  path: string;
  nodeId?: string;
}): string {
  const rootId = String(params.rootId || "").trim();
  const filePath = String(params.path || "").trim();
  if (!rootId || !filePath) {
    return "";
  }
  const nodeId = params.nodeId || getRootNodeId(rootId);
  const base = appBasePath(nodeId);
  const relative = filePath.split("/").filter(Boolean).map(encodeURIComponent).join("/");
  if (!relative) {
    return "";
  }
  // 账户段取决于「这个 URL 跟页面是否同源」，而 URL 又含账户段 ——
  // 先用占位段探一次同源关系（同源判定只看 origin，占位不影响结果）。
  const probe = `${base}/api/file/raw/x/${encodeURIComponent(rootId)}/${relative}`;
  const account = accountSegmentFor(probe);
  if (!account) {
    return "";
  }
  return `${base}/api/file/raw/${encodeURIComponent(account)}/${encodeURIComponent(rootId)}/${relative}`;
}
