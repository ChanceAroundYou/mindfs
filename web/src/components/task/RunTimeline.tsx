import React from "react";
import { useI18n } from "../../i18n/index";
import { RunNodeRunRow } from "./RunNodeRunRow";
import { isWaitingHumanGate } from "./runTimelineModel";
import type { NodeRunSnapshot, WorkflowRunSnapshot } from "./runTimelineTypes";

export type { NodeRunSnapshot, WorkflowRunNodeState, WorkflowRunSnapshot } from "./runTimelineTypes";

// WP-07 留言板下钻只读运行视图。
//
// 定位：留言板（TaskActivity / ContinueRequest）的一条留言在执行 workflow 时，
// 会产生一个 WorkflowRun 与其下的 NodeRun 序列。本组件是那条留言的**下钻子视图**——
// 只在留言板里点开某条留言后才出现，展示该留言触发的那次运行的节点时间线。
// 它不是留言板的平级 tab，也不是任务详情的平级 tab，不得直接挂到 TaskDetailPanel
// 的 tab 栏上（见 web/tests/run-timeline-drill-in.test.mjs 钉住的层级契约）。
//
// 展示层：不取数、不 import services、不发任何请求。session 跳转与 HumanGate
// 审批都通过回调交给父层（onOpenSession / onDecideGate），本组件自己不做路由，
// 也不决定重试或改写决定。单行渲染与状态配色拆在 RunNodeRunRow，本文件只做编排。
//
// 数据形状对齐后端 workflow_types.go（A5 `a571567d` 的 WorkflowRun/NodeRun）；
// 类型定义在 runTimelineTypes.ts，避免与行组件互相 import。

export type RunTimelineProps = {
  /** 本次下钻的 WorkflowRun 快照（留言板里点开的那条留言对应的运行）。 */
  run: WorkflowRunSnapshot | null | undefined;
  /** 该 run 下的 NodeRun 列表，按时间顺序（父层已排序）。 */
  nodeRuns?: NodeRunSnapshot[] | null;
  /** 节点 id → 显示名（来自 WorkflowRun.Graph 快照的节点名）；缺省时显示 node_id。 */
  nodeNames?: Record<string, string> | null;
  /** 节点 id → 节点类型（来自 WorkflowRun.Graph 快照）；审批入口只认 human_gate。 */
  nodeKinds?: Record<string, string> | null;
  /** session 跳转：父层负责打开会话（复用 onOpenSession）。 */
  onOpenSession?: (sessionKey: string) => void;
  /** HumanGate 决定：父层负责调用专用端点；缺省时视图保持纯只读。 */
  onDecideGate?: (nodeRun: NodeRunSnapshot, decision: "approve" | "reject") => void;
  /** 正在提交决定的 NodeRun id：两个按钮都禁用，避免重复点击。 */
  decidingNodeRunId?: string;
  /** 回到留言板的回调：下钻视图的“返回”入口，证明层级是子视图而非平级 tab。 */
  onBackToComments?: () => void;
};

export function RunTimeline({ run, nodeRuns, nodeNames, nodeKinds, onOpenSession, onDecideGate, decidingNodeRunId, onBackToComments }: RunTimelineProps) {
  const { t } = useI18n();
  const list: NodeRunSnapshot[] = Array.isArray(nodeRuns) ? nodeRuns : [];

  return (
    <div
      data-testid="run-timeline"
      data-drill-in="comment-run"
      style={{ display: "flex", flexDirection: "column", gap: "8px", padding: "4px 0" }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        {onBackToComments ? (
          <button
            type="button"
            onClick={onBackToComments}
            data-testid="run-timeline-back"
            style={{
              border: "none",
              background: "transparent",
              color: "var(--accent-color)",
              cursor: "pointer",
              fontSize: "12px",
              fontWeight: 700,
              padding: 0,
            }}
          >
            {t("task.runTimeline.backToComments")}
          </button>
        ) : null}
        <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
          {run ? `Run ${run.id}` : t("task.runTimeline.noRun")}
          {run?.version ? ` · ${run.version}` : ""}
          {typeof run?.revision === "number" ? ` · r${run.revision}` : ""}
        </span>
      </div>

      {list.length === 0 ? (
        <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.runTimeline.empty")}</div>
      ) : (
        <ol style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: "8px" }}>
          {list.map((nodeRun) => (
            <li key={nodeRun.id}>
              <RunNodeRunRow
                nodeRun={nodeRun}
                displayName={nodeNames?.[nodeRun.node_id] ?? nodeRun.node_id}
                isGate={isWaitingHumanGate(nodeRun, nodeKinds)}
                deciding={decidingNodeRunId === nodeRun.id}
                onOpenSession={onOpenSession}
                onDecideGate={onDecideGate}
              />
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}