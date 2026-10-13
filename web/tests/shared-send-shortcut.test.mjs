import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  formatSendShortcut,
  matchesSendShortcut,
  shortcutFromKeyboardEvent,
} from "../src/shared/sendShortcut.ts";

const keyboardEvent = (key, { altKey = false, ctrlKey = false, metaKey = false, shiftKey = false } = {}) => ({
  key,
  altKey,
  ctrlKey,
  metaKey,
  shiftKey,
});

test("send shortcut helpers normalize, validate, match, and format keyboard input", () => {
  assert.equal(shortcutFromKeyboardEvent(keyboardEvent("Control")), null);
  assert.deepEqual(shortcutFromKeyboardEvent(keyboardEvent("A", { ctrlKey: true })), {
    key: "a", alt: false, ctrl: true, meta: false, shift: false,
  });
  assert.deepEqual(shortcutFromKeyboardEvent(keyboardEvent(" ", { metaKey: true })), {
    key: "Space", alt: false, ctrl: false, meta: true, shift: false,
  });
  const shortcut = { key: "k", alt: true, ctrl: true, meta: false, shift: true };
  assert.equal(matchesSendShortcut(keyboardEvent("K", { altKey: true, ctrlKey: true, shiftKey: true }), shortcut), true);
  assert.equal(formatSendShortcut(shortcut), "Ctrl+Alt+Shift+K");
});

test("FileTree uses shared shortcut helpers while service retains compatible exports", () => {
  const root = new URL("../src/", import.meta.url);
  const view = rawReadFileSync(new URL("components/file/FileTree.tsx", root), "utf8");
  const service = rawReadFileSync(new URL("services/prefs/sendShortcut.ts", root), "utf8");
  assert.match(view, /from "\.\.\/\.\.\/shared\/sendShortcut"/);
  assert.doesNotMatch(view, /services\/prefs\/sendShortcut/);
  assert.match(service, /export \{[\s\S]*formatSendShortcut[\s\S]*\} from "\.\.\/\.\.\/shared\/sendShortcut"/);
});
