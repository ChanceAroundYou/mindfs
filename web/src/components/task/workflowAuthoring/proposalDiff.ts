import type { WorkflowGraph } from "../../../shared/workflowModel";

type NodeShape = WorkflowGraph["nodes"][number];

export type NodeChangeSides = { before: string; after: string };

function describeNode(node: NodeShape | undefined): string {
  if (!node) return "";
  try {
    return JSON.stringify(node);
  } catch {
    return String(node);
  }
}

// The server diff carries the before/after node state for every structural
// operation. Keep both sides so an update can be reviewed as a change instead
// of only as the proposed value; an add or remove leaves one side empty.
export function nodeChangeSides(before?: NodeShape, after?: NodeShape): NodeChangeSides {
  return { before: describeNode(before), after: describeNode(after) };
}