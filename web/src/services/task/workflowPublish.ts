import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { TaskActivity } from "../../shared/taskComments";

// 「固化 workflow」卡的专用决定端点（W3 T2）。
//
// 与 HumanGate 一样：普通留言端点不能批准它，这里也只发 activity_id / decision /
// note，不做任何 fallback。重复决定后端返回 409，调用方按错误展示，不重试。

export type WorkflowPublishDecision = "approve" | "reject";

export type WorkflowPublishInput = {
  activityId: string;
  decision: WorkflowPublishDecision;
  /** 附言：只随决定提交，写在卡片结果里，不会变成任务留言。 */
  note?: string;
};

export async function postTaskWorkflowPublish(
  rootId: string,
  taskId: string,
  input: WorkflowPublishInput,
  nodeId?: string,
): Promise<TaskActivity> {
  const trimmedNote = (input.note || "").trim();
  const payload = await protectedJSON<{ activity?: TaskActivity }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-publish`, undefined, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        root_id: rootId,
        activity_id: input.activityId,
        decision: input.decision,
        ...(trimmedNote ? { note: trimmedNote } : {}),
      }),
    },
  );
  if (!payload?.activity) throw new Error("workflow publish decision returned no activity");
  return payload.activity;
}
