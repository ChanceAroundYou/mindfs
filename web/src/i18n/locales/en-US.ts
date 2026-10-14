import { enUSSessions } from "./en-US-sessions";
import { enUSTasks } from "./en-US-tasks";
import { enUSProjects } from "./en-US-projects";
import { enUSAgents } from "./en-US-agents";
import { enUSWorkspace } from "./en-US-workspace";
export const enUS = {
  ...enUSSessions,
  ...enUSTasks,
  ...enUSProjects,
  ...enUSAgents,
  ...enUSWorkspace,
} as const;
