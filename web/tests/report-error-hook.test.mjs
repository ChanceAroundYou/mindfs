import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useReportError.ts"), "utf8");
for (const name of ["SessionViewerTimeline.tsx", "SessionViewerSlashCommand.tsx"]) {
  const source = fs.readFileSync(path.resolve(import.meta.dirname, `../src/components/session/${name}`), "utf8");
  assert.match(source, /useReportError\(\)/);
  assert.doesNotMatch(source, /services\/net\/error/);
}
assert.match(hook, /reportError\(code, message, options\)/);

console.log("report error hook contract ok");
