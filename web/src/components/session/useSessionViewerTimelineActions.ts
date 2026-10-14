import { isPersistedSeq } from "../../services/session";
import { savePrompt } from "../../services/prompts";

export function useSessionViewerTimelineActions() {
  return { isPersistedSeq, savePrompt };
}
