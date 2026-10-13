import type { Locale } from "../../i18n/index";
import type { ScheduledAgentTask } from "../../services/scheduledTasks";

export {
  menuButtonStyle,
  strategyButtonStyle,
} from "../../shared/styles";

export type FormState = {
  name: string;
  enabled: boolean;
  task_cron: string;
  agent: string;
  model: string;
  mode: string;
  effort: string;
  fast_service: "" | "on" | "off";
  prompt: string;
  new_session_cron: string;
};

export const emptyForm = (agent = ""): FormState => ({
  name: "",
  enabled: true,
  task_cron: "0 9 * * 1-5",
  agent,
  model: "",
  mode: "",
  effort: "",
  fast_service: "",
  prompt: "",
  new_session_cron: "",
});

export function formatTime(value: string | undefined, locale: Locale, neverRun: string): string {
  if (!value) return neverRun;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(locale, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function taskToForm(task: ScheduledAgentTask): FormState {
  return {
    name: task.name || "",
    enabled: !!task.enabled,
    task_cron: task.task_cron || "",
    agent: task.agent || "",
    model: task.model || "",
    mode: task.mode || "",
    effort: task.effort || "",
    fast_service: task.fast_service || "",
    prompt: task.prompt || "",
    new_session_cron: task.new_session_cron || "",
  };
}

export {
  menuButtonStyle,
  strategyButtonStyle,
} from "../../shared/styles";
