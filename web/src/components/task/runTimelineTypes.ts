// 运行视图的展示类型。
//
// 数据形状只有一份真相：shared/taskComments.ts 里的 WorkflowRunPayload /
// NodeRunPayload（对齐后端 workflow_types.go）。这里只给展示层起别名，
// 不在组件目录里再抄一份字段表 —— 两处各写一遍必然漂移。

import type { NodeRunPayload } from "../../shared/taskComments";

export type NodeRunSnapshot = NodeRunPayload;