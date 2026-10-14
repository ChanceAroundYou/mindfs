import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("file and session views delegate service work to colocated hooks", () => {
  const editor = read("../src/components/file/FileEditor.tsx");
  const editorHook = read("../src/components/file/useFileEditorReload.ts");
  const icons = read("../src/components/file/icons.tsx");
  const pushHook = read("../src/components/file/useWebPushMenu.ts");
  const session = read("../src/components/session/SessionViewer.tsx");
  const sessionHook = read("../src/components/session/useSessionViewerExchangeProjection.ts");

  assert.equal(editor.includes("services/file"), false);
  assert.match(editor, /useFileEditorReload\(\)/);
  assert.match(editorHook, /fetchFile\(/);
  assert.equal(icons.includes("services/webPush"), false);
  assert.match(icons, /useWebPushMenu\(\)/);
  assert.match(pushHook, /getWebPushStatus/);
  assert.match(pushHook, /subscribeWebPush/);
  assert.match(pushHook, /unsubscribeWebPush/);
  assert.equal(session.includes("services/session"), false);
  assert.match(session, /useSessionViewerExchangeProjection/);
  assert.match(sessionHook, /mergeSessionExchanges/);
  assert.match(sessionHook, /isPersistedSeq/);
});
