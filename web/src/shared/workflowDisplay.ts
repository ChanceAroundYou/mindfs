// 工作流的展示名。
//
// 活动上存的是 `workflow_version_id`，格式为 `<workflowID>:v<N>`（例如
// `workflow-blank:v1`）。界面上只该出现用户起的名字，版本号是内部标识，不展示。

function workflowVersionWorkflowID(versionId: string | undefined): string {
  return `${versionId || ""}`.replace(/:v\d+$/, "");
}

export function workflowDisplayName(
  versionId: string | undefined,
  names: Record<string, string> | null | undefined,
): string {
  const id = workflowVersionWorkflowID(versionId);
  if (!id || !names) return "";
  return `${names[id] || ""}`.trim();
}