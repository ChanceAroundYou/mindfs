import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (relative) => readFileSync(join(webRoot, relative), "utf8");

test("async state message defines distinct loading, empty, and error semantics", () => {
  const component = read("src/components/common/AsyncStateMessage.tsx");
  assert.match(component, /kind === null\) return null/);
  assert.match(component, /kind === "loading" \? "status" : kind === "error" \? "alert"/);
  assert.match(component, /\.\.\.style/);
});

test("common async state component is reused in three list views", () => {
  for (const file of [
    "src/components/root/RootWorktreeContentView.tsx",
    "src/components/file/ProjectAddPopover.tsx",
    "src/components/task/ScheduledAgentTaskList.tsx",
  ]) {
    const source = read(file);
    assert.match(source, /AsyncStateMessage/);
    assert.match(source, /kind="loading"/);
    assert.match(source, /kind="empty"/);
  }
  assert.match(read("src/components/root/RootWorktreeContentView.tsx"), /kind="error"/);
  assert.match(read("src/components/file/ProjectAddPopover.tsx"), /kind="error"/);
});
