import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const appSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/App.tsx"),
  "utf8",
);

assert.match(
  appSource,
  /addEventListener\(["']visibilitychange["'][\s\S]*?refreshMultiProjectReplyingSessions/,
  "pending reconciliation must run when the page becomes visible",
);
assert.match(
  appSource,
  /addEventListener\(["']pageshow["'][\s\S]*?refreshMultiProjectReplyingSessions/,
  "pending reconciliation must run when a suspended page is restored",
);

console.log("pending-recovery-on-resume.test.mjs ok");
