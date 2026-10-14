import { zhCNSessions } from "./zh-CN-sessions";
import { zhCNTasks } from "./zh-CN-tasks";
import { zhCNProjects } from "./zh-CN-projects";
import { zhCNAgents } from "./zh-CN-agents";
import { zhCNWorkspace } from "./zh-CN-workspace";
export const zhCN = {
  ...zhCNSessions,
  ...zhCNTasks,
  ...zhCNProjects,
  ...zhCNAgents,
  ...zhCNWorkspace,
} as const;
