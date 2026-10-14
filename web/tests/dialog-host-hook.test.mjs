import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("dialog-host-hook", async () => {
const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useDialogHost.ts"), "utf8");
const host = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/shell/DialogHost.tsx"), "utf8");

assert.match(hook, /dialogService\.subscribe/);
assert.match(hook, /dialogService\.dismiss\(\)/);
assert.match(hook, /dialogService\.resolve\(value\)/);
assert.match(hook, /useBackLayer\(!!request, dismiss\)/);
assert.match(hook, /request\.kind === "confirm"/);
assert.match(host, /useDialogHost\(\)/);
assert.doesNotMatch(host, /services\/dialog/);

console.log("dialog host hook contract ok");
});
