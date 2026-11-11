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
test("create authoring has its own gateway and a separate atomic apply", () => {
  const service = read("src/services/task/workflowDraft.ts");
  const hook = read("src/components/task/useWorkflowAuthoring.ts");
  const review = read("src/components/task/useWorkflowAuthoringReview.ts");

  assert.match(service, /export async function generateWorkflowCreateProposal\(/, "create generation service is exported");
  assert.match(service, /export async function reviewWorkflowCreatePatch\(/, "create subset review service is exported");
  assert.match(service, /export async function applyWorkflowCreateProposal\(/, "create apply service is exported");

  const generate = service.match(/export async function generateWorkflowCreateProposal[\s\S]*?\n}/)?.[0] || "";
  assert.match(generate, /controlPath\("\/api\/workflows\/authoring\/proposals"\)/, "create generation stays on the control plane");
  assert.match(generate, /workflow_id/, "create generation names the draft to create");
  assert.match(generate, /"r0"|r0/, "create generation pins the synthetic base revision");
  assert.doesNotMatch(generate, /patch\/apply|publish|confirmWorkflowPublishProposal/, "generation never persists");

  const validate = service.match(/export async function reviewWorkflowCreatePatch[\s\S]*?\n}/)?.[0] || "";
  assert.match(validate, /\/api\/workflows\/authoring\/validate/, "create review revalidates the selected subset");
  assert.doesNotMatch(validate, /patch\/apply|publish/, "create review never persists");

  const apply = service.match(/export async function applyWorkflowCreateProposal[\s\S]*?\n}/)?.[0] || "";
  assert.match(apply, /\/api\/workflows\/authoring\/apply/, "create apply uses the atomic create endpoint");

  assert.match(hook, /generateWorkflowCreateProposal\(/, "create scope calls the create gateway");
  assert.match(review, /reviewWorkflowCreatePatch\(/, "create review revalidates before apply");
  assert.match(review, /applyWorkflowCreateProposal\(/, "create apply is the only write");
});
