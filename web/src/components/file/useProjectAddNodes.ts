import { getNodes } from "../../services/net/nodeRegistry";

/** Keep node registry access in the project-add logic boundary. */
export function useProjectAddNodes() {
  return getNodes();
}
