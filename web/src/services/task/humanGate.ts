import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { NodeRunPayload } from "../../shared/taskComments";

// HumanGate 的专用决定端点（后端 D4）。
//
// 普通留言端点（/comments）永远不能批准网关；这里刻意只发 root_id / node_id /
// decision（+ 可选 comment_id），不做任何 fallback。重复同一决定由后端幂等，
// 相反决定返回 409 —— 调用方按错误展示，不重试、不改写决定。

export type HumanGateDecision = "approve" | "reject";

export type HumanGateInput = {
  /** workflow 图里的节点 id（不是 mindfs 机器节点 id）。 */
  workflowNodeId: string;
  decision: HumanGateDecision;
  commentId?: string;
};

export async function postTaskHumanGate(
  rootId: string,
  taskId: string,
  input: HumanGateInput,
  nodeId?: string,
): Promise<NodeRunPayload> {
  const payload = await protectedJSON<{ node_run?: NodeRunPayload }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/human-gate`, undefined, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        root_id: rootId,
        node_id: input.workflowNodeId,
        decision: input.decision,
        ...(input.commentId ? { comment_id: input.commentId } : {}),
      }),
    },
  );
  if (!payload?.node_run) throw new Error("human gate decision returned no node run");
  return payload.node_run;
}