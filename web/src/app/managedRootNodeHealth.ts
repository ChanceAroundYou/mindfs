/**
 * 取一台节点的项目列表，并把这一次请求的结果记进节点健康表。
 *
 * 从 `useManagedRoots` 抽出来有两个原因：那个文件已经贴着 500 行硬线；更重要的是
 * 「发一次请求」和「记一次健康」必须永远成对 —— 分开写迟早有人只改一半，于是
 * 失败被记成成功、或者干脆没人记，界面又开始把「连不上」显示成「没有项目」。
 */
import { protectedJSON, withNodeRetry } from "../services/net/api";
import { appPath } from "../services/net/base";
import { markNodeReachable, markNodeTableReachable, markNodeTableUnreachable, markNodeUnreachable } from "../services/net/nodeHealth";
import { syncNodesFromServerWithOutcome } from "../services/net/nodeRegistry";
import type { ManagedRootPayload } from "./appMisc";

export type NodeLoadFailure = { id: string; name: string };

/**
 * 节点表（主节点上的 `nodes.json`）同步一次。
 *
 * 失败不再静默吞掉：这份清单在主节点上，拉不到意味着屏幕上的节点列表可能不是最新
 * （少了刚添加的、或还留着已删除的），继续拿它当事实渲染就是在把「不知道」说成「没有」。
 *
 * 判成败必须看 `source`，不能看有没有抛异常 —— `syncNodesFromServer` 失败时退回缓存
 * 或本地兜底，**照常返回一份列表**。早先这里等它不抛就当成功，于是主节点整个不可达时
 * 界面拿到的是「拉到了」，转头拿那份陈旧清单宣布「所有节点都连不上」。
 *
 * `force` 不再跳过一次：强制刷新恰恰是最需要知道成败的时候，而且同一次请求由
 * `syncNodesFromServer` 自己的在途 promise 合并，重复调用不会多发。
 */
export async function syncNodeTableHealth(_force?: boolean): Promise<void> {
  const outcome = await syncNodesFromServerWithOutcome();
  if (outcome.source === "server") {
    markNodeTableReachable();
    return;
  }
  const err = outcome.error;
  markNodeTableUnreachable((err as Error)?.message || String(err ?? "node table unavailable"));
}

/** 取一台节点的项目列表；成功/失败都记健康，失败返回空列表但由调用方保留旧条目。 */
export async function loadNodeManagedRoots(
  node: { id: string; name?: string; color?: string },
  failures: NodeLoadFailure[],
): Promise<ManagedRootPayload[]> {
  try {
    const dirs = await withNodeRetry(() => protectedJSON<ManagedRootPayload[]>(appPath("/api/dirs", node.id)));
    markNodeReachable(node.id);
    return (Array.isArray(dirs) ? dirs : []).map((d: any) => ({
      ...d,
      _nodeId: (d as any)._nodeId || node.id,
      _nodeColor: node.color,
      _nodeName: node.name,
    }));
  } catch (err) {
    // 空列表不等于「它没有项目」：健康状态单独记一份，界面据此区分「真空」与「连不上」。
    markNodeUnreachable(node.id, (err as Error)?.message || String(err));
    failures.push({ id: String(node.id), name: String(node.name || node.id) });
    return [] as ManagedRootPayload[];
  }
}