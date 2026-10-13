import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");
const readPhysical = (path) => {
  const fd = openSync(resolve(root, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};

test("send message keeps the coordinator below the physical hard line and preserves stage order", () => {
  const send = readPhysical("src/app/useSendMessage.ts");
  const target = read("src/app/prepareSendMessageTarget.ts");
  const slash = read("src/app/useSendMessageSlashCommand.ts");
  const types = read("src/app/useSendMessageTypes.ts");

  assert.ok(send.split("\n").length - 1 < 500, "send-message coordinator must be below the 500-line hard line");
  assert.match(send, /prepareSendMessageTarget\(params,/);
  assert.match(send, /useSendMessageSlashCommand\(params\)/);
  assert.match(send, /runSlashCommand\([\s\S]*?clearSlashCommandResultForSession[\s\S]*?const userEx: Exchange[\s\S]*?buildClientContext[\s\S]*?sessionService\.sendMessage/);
  assert.match(target, /resolveLockedSessionKey[\s\S]*?resolvePendingForSession[\s\S]*?setBoundSessionForRoot[\s\S]*?plan_mode: pendingPlanMode \|\| messageRequestsPlanMode/);
  assert.match(slash, /transientSlashCommand[\s\S]*?sessionService\.runSlashCommand[\s\S]*?status: "failed"/);
  assert.match(types, /export type SendMessageParams/);
  assert.match(send, /export type \{ SendMessageParams \}/);
});
