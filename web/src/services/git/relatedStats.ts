import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import { gitScopedRoot } from "./history";

export type RelatedFileStatBatchTarget = {
  /** 调用方生成的不透明键，原样回传 —— 服务端跳过某些条目时结果才不会错位。 */
  id: string;
  path: string;
  head?: string;
  repo_path?: string;
  repo_kind?: string;
};

export type RelatedFileStatBatchValue = {
  status: string;
  additions: number;
  deletions: number;
};

// 批量统计的 in-flight 复用：会话视图与 App 顶层各挂一份 useRelatedFileStats，
// 两边要的是同一批文件，没有这层就会把同一批请求发两遍。
const relatedFileStatsInflight = new Map<string, Promise<Record<string, RelatedFileStatBatchValue>>>();

function relatedFileStatsBatchKey(rootId: string, targets: RelatedFileStatBatchTarget[], nodeId?: string): string {
  const ids = targets.map((target) => target.id).sort();
  return `${gitScopedRoot(rootId, nodeId)}\u0000${ids.join("\u0001")}`;
}

/**
 * 批量取关联文件的增删统计，不取 diff 正文。
 *
 * 存在的理由：徽标只需要 status/additions/deletions，而逐文件调
 * fetchGitRelatedFileDiff 会为每个文件拉一份完整 diff —— 实测 80 个文件时
 * 响应 6473B 里 5073B 是没人读的正文，且每 30–60 秒因 git status 变化重来一遍。
 *
 * 返回以 target.id 为键的表；服务端判定「自基线以来无变更」的条目（status 为空）
 * 与请求失败的条目都不出现在结果里 —— 与逐文件写法里 `if (!diff.status) return null`
 * 的观感一致。
 */
export async function fetchRelatedFileStatsBatch(
  rootId: string,
  targets: RelatedFileStatBatchTarget[],
  nodeId?: string,
): Promise<Record<string, RelatedFileStatBatchValue>> {
  if (!rootId || targets.length === 0) {
    return {};
  }
  const scopedNodeId = nodeId || getRootNodeId(rootId);
  const key = relatedFileStatsBatchKey(rootId, targets, scopedNodeId);
  const existing = relatedFileStatsInflight.get(key);
  if (existing) {
    return existing;
  }
  const promise = protectedJSON<any>(
    appURL("/api/git/related-files/stats", undefined, scopedNodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        root: rootId,
        targets: targets.map((target) => ({
          id: target.id,
          path: target.path,
          head: target.head || "",
          repo_path: target.repo_path || "",
          repo_kind: target.repo_kind || "",
        })),
      }),
    },
  )
    .then((payload: any) => {
      const result: Record<string, RelatedFileStatBatchValue> = {};
      const stats = Array.isArray(payload?.stats) ? payload.stats : [];
      for (const entry of stats) {
        const id = typeof entry?.id === "string" ? entry.id : "";
        const status = typeof entry?.status === "string" ? entry.status : "";
        if (!id || !status) {
          continue;
        }
        result[id] = {
          status,
          additions: Number(entry?.additions) || 0,
          deletions: Number(entry?.deletions) || 0,
        };
      }
      return result;
    })
    .finally(() => {
      relatedFileStatsInflight.delete(key);
    });
  relatedFileStatsInflight.set(key, promise);
  return promise;
}
