import type { StageTemplate, TaskTemplate } from "./types";
import { protectedJSON } from "../net/api";
import { controlPath } from "../net/controlPlane";

// 模板是**控制面**：库只有主节点一份（见 services/controlPlane.ts）。
// 用 controlPath 而不是 appURL(..., nodeId) —— 后者会让模板跟着项目所在节点走，
// 于是每台机器各存一份模板（实测 Local 3 个 / PC 5 个，各有对方没有的）。
//
// rootId 只用来**筛选**（全局 + 该项目），不决定请求打去哪：项目可能分布在
// 多台节点上，而模板库只有主节点有，所以「项目在 pc」不能推出「模板在 pc」。
// 阶段模板同样属控制面（库只在主节点），但**不带项目筛选**：阶段模板是模板的
// 组成部分，本身没有项目归属。这三个函数目前无调用方（阶段编辑走任务的 add-stage），
// 保留是为了 API 面完整，改动保持最小。
export async function fetchStageTemplates(nodeId?: string): Promise<StageTemplate[]> {
  const payload = await protectedJSON<any>(controlPath("/api/task-stage-templates"));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function saveStageTemplate(template: StageTemplate, nodeId?: string): Promise<StageTemplate> {
  return protectedJSON<StageTemplate>(controlPath("/api/task-stage-templates"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(template),
  });
}

export async function deleteStageTemplate(id: string, nodeId?: string): Promise<void> {
  await protectedJSON(controlPath(`/api/task-stage-templates/${encodeURIComponent(id)}`), {
    method: "DELETE",
  });
}

export async function fetchTaskTemplates(rootId?: string): Promise<TaskTemplate[]> {
  const params = rootId ? new URLSearchParams({ root: rootId }) : undefined;
  const payload = await protectedJSON<any>(controlPath("/api/task-templates", params));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function saveTaskTemplate(template: TaskTemplate): Promise<TaskTemplate> {
  const path = template.id ? `/api/task-templates/${encodeURIComponent(template.id)}` : "/api/task-templates";
  return protectedJSON<TaskTemplate>(controlPath(path), {
    method: template.id ? "PUT" : "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(template),
  });
}

export async function deleteTaskTemplate(id: string): Promise<void> {
  await protectedJSON(controlPath(`/api/task-templates/${encodeURIComponent(id)}`), {
    method: "DELETE",
  });
}
