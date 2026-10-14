import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("report-error-hook", async () => {
const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useReportError.ts"), "utf8");
for (const name of ["SessionViewerTimeline.tsx", "SessionViewerSlashCommand.tsx"]) {
  const source = fs.readFileSync(path.resolve(import.meta.dirname, `../src/components/session/${name}`), "utf8");
  assert.match(source, /useReportError\(\)/);
  assert.doesNotMatch(source, /services\/net\/error/);
}
assert.match(hook, /reportError\(code, message, options\)/);

console.log("report error hook contract ok");
});
