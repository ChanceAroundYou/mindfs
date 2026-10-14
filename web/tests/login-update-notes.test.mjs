import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("login update notes keep their toggle and backend-upgrade notice together", () => {
  const parent = read("../src/components/account/Login.tsx");
  const notes = read("../src/components/account/LoginUpdateNotes.tsx");
  assert.match(parent, /<LoginUpdateNotes[\s\S]{0,200}?onToggle=\{\(\) => setAppUpdateNotesOpen/);
  assert.match(notes, /if \(!notes\) return null/);
  assert.match(notes, /open \? t\("login\.hideUpdateNotes"\) : t\("login\.showUpdateNotes"\)/);
  assert.match(notes, /t\("login\.backendUpgradeRequired"\)/);
  assert.match(notes, /\{open \? \(/);
});
