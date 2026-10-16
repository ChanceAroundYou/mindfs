import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";

// 「换会话工作目录」这一条 HTTP 路径。
//
// 单独成文件而不是塞进 sessionApi.ts：那边已经贴着 500 行硬线，且这一条与其余
// 会话读写不是一类 —— 它不读会话、不写列表缓存，只让服务端把转录搬走并把 cwd 钉在
// 新目录上（见 AGENTS.md 事实 14）。调用方只有标题栏菜单一处。
export async function repointSession(
  rootId: string,
  sessionKey: string,
  targetPath: string,
  nodeId?: string,
): Promise<{ session_key: string; agent_session_id: string } | null> {
  nodeId = nodeId || getRootNodeId(rootId);
  const params = new URLSearchParams({ root: rootId });
  // 失败原样抛出：路径不存在 / 已经在目标目录 / 找不到转录，都是用户要看懂的
  // 具体原因，不能被吞成一个 null。
  return await protectedJSON<{ session_key: string; agent_session_id: string }>(
    appURL(`/api/sessions/${encodeURIComponent(sessionKey)}/repoint`, params, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ root_id: rootId, target_path: targetPath }),
    },
  );
}
