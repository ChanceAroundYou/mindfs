import React from "react";
import { useI18n } from "../../i18n/index";
import { RunNodeRunRow } from "./RunNodeRunRow";
import { isWaitingHumanGate } from "./runTimelineModel";
import type { NodeRunSnapshot } from "./runTimelineTypes";

export type { NodeRunSnapshot } from "./runTimelineTypes";

// 对话流里的运行结果：只回答「这次跑到哪、结果是什么」。
//
// 不再有 run id / 版本 / revision 标题，也没有「返回留言板」按钮——它就长在
// 对话流下面，折叠由外层入口负责。每行只保留节点名、状态、结果（可读文本/
// 变更清单/验证字段）、错误与人工审批动作，移动端一屏能看完。
//
// 展示层：不取数、不 import services、不发任何请求。session 跳转与 HumanGate
// 审批都通过回调交给父层（onOpenSession / onDecideGate）。

export type RunTimelineProps = {
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
};

export function RunTimeline({ nodeRuns, nodeNames, nodeKinds, onOpenSession, onDecideGate, decidingNodeRunId }: RunTimelineProps) {
  const { t } = useI18n();
  const list: NodeRunSnapshot[] = Array.isArray(nodeRuns) ? nodeRuns : [];

  if (list.length === 0) {
    return (
      <div data-testid="run-timeline" data-drill-in="comment-run" style={containerStyle}>
        <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>{t("task.runTimeline.empty")}</div>
      </div>
    );
  }

  return (
    <div data-testid="run-timeline" data-drill-in="comment-run" style={containerStyle}>
      <ol style={listStyle}>
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
    </div>
  );
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  padding: "2px 0",
  minWidth: 0,
};

const listStyle: React.CSSProperties = {
  listStyle: "none",
  margin: 0,
  padding: 0,
  display: "flex",
  flexDirection: "column",
  gap: 2,
};
