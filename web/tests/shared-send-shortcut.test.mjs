import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  formatSendShortcut,
  matchesSendShortcut,
  resolveEnterIntent,
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

const enterIntent = (event, options = {}) => resolveEnterIntent(event, {
  isMobile: false,
  mobileEnterKeySends: false,
  sendShortcut: null,
  ...options,
});

test("desktop: Enter inserts a newline, Ctrl/Cmd+Enter sends", () => {
  assert.equal(enterIntent(keyboardEvent("Enter")), "newline");
  assert.equal(enterIntent(keyboardEvent("Enter", { shiftKey: true })), "newline");
  assert.equal(enterIntent(keyboardEvent("Enter", { altKey: true })), "newline");
  assert.equal(enterIntent(keyboardEvent("NumpadEnter")), "newline");
  assert.equal(enterIntent(keyboardEvent("Enter", { ctrlKey: true })), "send");
  assert.equal(enterIntent(keyboardEvent("Enter", { metaKey: true })), "send");
  // 非回车键一律交回编辑器（方向键、字符键都不能被当成发送）。
  assert.equal(enterIntent(keyboardEvent("a")), "newline");
  // 没有事件（合成调用）按换行处理，绝不误发。
  assert.equal(enterIntent(null), "newline");
});

test("mobile: Enter follows the \"enter sends\" preference, send button always available", () => {
  // 默认关：软键盘回车换行，右下角发送键发送 —— 与聊天软键盘惯例一致。
  assert.equal(enterIntent(keyboardEvent("Enter"), { isMobile: true }), "newline");
  assert.equal(enterIntent(keyboardEvent("Enter"), { isMobile: true, mobileEnterKeySends: true }), "send");
  // 移动端接外接键盘时 Ctrl/Cmd+Enter 同样可用。
  assert.equal(enterIntent(keyboardEvent("Enter", { ctrlKey: true }), { isMobile: true }), "send");
  assert.equal(enterIntent(keyboardEvent("Enter", { shiftKey: true }), { isMobile: true, mobileEnterKeySends: true }), "newline");
});

test("custom send shortcut wins on both desktop and mobile", () => {
  const shortcut = { key: "s", alt: false, ctrl: true, meta: false, shift: false };
  assert.equal(enterIntent(keyboardEvent("Enter"), { sendShortcut: shortcut }), "newline");
  assert.equal(enterIntent(keyboardEvent("s", { ctrlKey: true }), { sendShortcut: shortcut }), "newline");
  // 自定义快捷键不吞回车以外的键：解析器只看 Enter，快捷键由外层 matchesSendShortcut 处理。
  // 但若用户把发送键录成 Shift+Enter，则 Shift+Enter 变为发送。
  const shiftEnter = { key: "Enter", alt: false, ctrl: false, meta: false, shift: true };
  assert.equal(enterIntent(keyboardEvent("Enter", { shiftKey: true }), { sendShortcut: shiftEnter }), "send");
});
