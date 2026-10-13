import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  CANDIDATE_FETCH_DEBOUNCE_MS,
  chatBlurPlaceholderKeys,
  getSelectionPreview,
  modePlaceholderKeys,
  wsStatusMeta,
} from "../src/shared/actionBarUtils.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("action bar shared presentation helpers preserve messages and timing contracts", () => {
  assert.equal(CANDIDATE_FETCH_DEBOUNCE_MS, 512);
  assert.equal(modePlaceholderKeys.chat, "action.placeholder.chat");
  assert.deepEqual(chatBlurPlaceholderKeys, ["action.placeholder.chat", "action.placeholder.tip"]);
  assert.equal(getSelectionPreview("  abcdef  "), "abc...");
  assert.equal(getSelectionPreview(""), "...");
  assert.equal(wsStatusMeta("connected", (key) => key).label, "action.ws.connected");
  assert.equal(wsStatusMeta("reconnecting", (key) => key).color, "#ef4444");
});

test("ActionBar logic uses shared contracts and does not import view modules", () => {
  const logic = read("../src/components/action/useActionBar.ts");
  const types = read("../src/components/action/actionTypes.ts");
  const editor = read("../src/components/editor/TokenEditor.tsx");
  const mode = read("../src/components/agent/ModeSelector.tsx");
  const attachments = read("../src/components/action/AttachmentsArea.tsx");
  const styleFacade = read("../src/components/action/styleHelpers.ts");
  const appStorage = read("../src/app/appStorage.ts");

  assert.doesNotMatch(logic, /from "(?:\.\.\/editor\/TokenEditor|\.\.\/agent\/ModeSelector|\.\/AttachmentsArea|\.\/styleHelpers)"/);
  assert.doesNotMatch(types, /from "\.\.\/agent\/ModeSelector"/);
  assert.match(editor, /export type \{ CandidateType, TokenEditorHandle \} from "\.\.\/\.\.\/shared\/actionBarTypes"/);
  assert.match(mode, /export type \{ SessionMode \} from "\.\.\/\.\.\/shared\/actionBarTypes"/);
  assert.match(attachments, /export type \{ PendingAttachment \} from "\.\.\/\.\.\/shared\/actionBarTypes"/);
  assert.match(styleFacade, /export \* from "\.\.\/\.\.\/shared\/actionBarUtils"/);
  assert.match(styleFacade, /export \{ useResponsive \} from "\.\.\/\.\.\/hooks\/useResponsive"/);
  assert.match(appStorage, /export \{ CANDIDATE_FETCH_DEBOUNCE_MS \} from "\.\.\/shared\/actionBarUtils"/);
});

test("ActionBar handler flow is colocated and the controller meets the hard line", () => {
  const controller = read("../src/components/action/useActionBar.ts");
  const handlers = read("../src/components/action/useActionBarHandlers.ts");
  const controllerLines = controller.split("\n").length;
  const handlerLines = handlers.split("\n").length;

  assert.ok(controllerLines <= 500, `useActionBar.ts is ${controllerLines} lines`);
  assert.ok(handlerLines <= 500, `useActionBarHandlers.ts is ${handlerLines} lines`);
  assert.match(controller, /useActionBarHandlers\(/);
  assert.match(handlers, /export function useActionBarHandlers/);
  assert.match(handlers, /parsePlanCommand/);
  assert.match(handlers, /uploadFiles\(/);
  assert.match(handlers, /matchesSendShortcut\(/);
  assert.match(handlers, /appendPendingAttachments\(imageFiles\)/);
  assert.doesNotMatch(handlers, /from ["'][^"']+\/(?:TokenEditor|ModeSelector|AttachmentsArea|ActionBar\.tsx)/);
});
