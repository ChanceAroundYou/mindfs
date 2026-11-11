import assert from "node:assert/strict";
import path from "node:path";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("workflow authoring generation is read-only until the user applies", () => {
  const service = read("src/services/task/workflowDraft.ts");
  const hook = read("src/components/task/useWorkflowAuthoring.ts");

  assert.match(service, /export async function generateWorkflowDraftProposal\(/, "Draft generation service is exported");
  assert.match(service, /export async function generateWorkflowRunProposal\(/, "Run generation service is exported");

  const draftFn = service.match(/export async function generateWorkflowDraftProposal[\s\S]*?\n}/)?.[0] || "";
  assert.match(draftFn, /controlPath\(/, "Draft authoring stays on the control plane");
  assert.match(draftFn, /\/authoring\/proposals/, "Draft authoring uses the authoring gateway");
  assert.match(draftFn, /base_revision/, "generation pins the exact revision");
  assert.match(draftFn, /instruction/, "generation carries the natural-language goal");
  assert.match(draftFn, /agent/, "generation names the harness");
  assert.doesNotMatch(draftFn, /patch\/apply|publish|confirmWorkflowPublishProposal/, "generation never persists");

  const runFn = service.match(/export async function generateWorkflowRunProposal[\s\S]*?\n}/)?.[0] || "";
  assert.match(runFn, /appURL\(/, "Run authoring follows the task's data-plane node");
  assert.match(runFn, /root_id/, "Run authoring passes the project root");
  assert.match(runFn, /\/authoring\/proposals/, "Run authoring uses the authoring gateway");
  assert.doesNotMatch(runFn, /patch\/apply|publish|confirmWorkflowPublishProposal/, "Run generation never persists");

  assert.match(hook, /useWorkflowAuthoring/, "the authoring hook is the single state owner");
  assert.match(hook, /generateWorkflowDraftProposal\(/, "Draft scope calls the Draft gateway");
  assert.match(hook, /generateWorkflowRunProposal\(/, "Run scope calls the Run gateway");
  assert.match(hook, /type: "begin"/, "generation starts a revision-bound attempt");
  assert.match(hook, /type: "observe-revision"/, "snapshot changes mark the proposal stale");
  assert.match(hook, /type: "validation"/, "server validate and dry-run feed the review state");
  assert.match(hook, /type: "reject"/, "generation failures surface structured issues");
  assert.match(hook, /workflowPatchIssues\(/, "rejections reuse the structured issue parser");
  assert.doesNotMatch(hook, /applyWorkflowDraftPatch\(|publishWorkflowDraft\(|confirmWorkflowPublishProposal\(/,
    "generation hook cannot apply or publish");
});