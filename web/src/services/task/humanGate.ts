import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { HumanGateDecision, NodeRunPayload } from "../../shared/taskComments";

// HumanGate 的专用决定端点（后端 D4）。
//
// 普通留言端点（/comments）永远不能批准网关；这里刻意只发 root_id / node_id /
// decision（+ 可选 comment_id），不做任何 fallback。重复同一决定由后端幂等，
// 相反决定返回 409 —— 调用方按错误展示，不重试、不改写决定。

export type { HumanGateDecision };

export type HumanGateInput = {
  /** workflow 图里的节点 id（不是 mindfs 机器节点 id）。 */
  workflowNodeId: string;
  decision: HumanGateDecision;
  commentId?: string;
  /** 审批附言：只在批准时随请求提交，后端转成排队用户留言交给 Router。 */
  note?: string;
};

export async function postTaskHumanGate(
  rootId: string,
  taskId: string,
  input: HumanGateInput,
  nodeId?: string,
): Promise<NodeRunPayload> {
  // 空附言必须整键省略：后端把「有 note」当作用户真的留了话，空串会让它在
  // 时间线上多出一条空留言。
  const trimmedNote = (input.note || "").trim();
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
        ...(trimmedNote ? { note: trimmedNote } : {}),
      }),
    },
  );
  if (!payload?.node_run) throw new Error("human gate decision returned no node run");
  return payload.node_run;
}