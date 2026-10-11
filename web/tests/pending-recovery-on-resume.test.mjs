import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 2026-10-10 分层聚合：这两个 effect 已从 App.tsx 搬进 useReplyingSessionPoll.ts。
const pollSource = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/app/useReplyingSessionPoll.ts"),
  "utf8",
);

assert.match(
  pollSource,
  /addEventListener\(["']visibilitychange["'][\s\S]*?refreshMultiProjectReplyingSessions/,
  "pending reconciliation must run when the page becomes visible",
);
assert.match(
  pollSource,
  /addEventListener\(["']pageshow["'][\s\S]*?refreshMultiProjectReplyingSessions/,
  "pending reconciliation must run when a suspended page is restored",
);

console.log("pending-recovery-on-resume.test.mjs ok");
