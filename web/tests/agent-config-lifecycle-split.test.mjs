import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("agent lifecycle view uses shared config styles and retains its compatibility export", () => {
  const facade = read("../src/components/file/AgentConfigPopover.tsx");
  const lifecycle = read("../src/components/file/AgentLifecyclePopover.tsx");
  const styles = read("../src/components/file/agentConfigStyles.ts");
  assert.match(facade, /export \{ AgentLifecyclePopover \} from "\.\/AgentLifecyclePopover"/);
  assert.match(lifecycle, /export function AgentLifecyclePopover/);
  assert.match(lifecycle, /item\.installed \? "update" : "install"/);
  assert.match(lifecycle, /expandedDescriptions\.has\(item\.name\)/);
  assert.match(lifecycle, /agentConfigPrimaryButtonStyle\(disabled\)/);
  assert.match(styles, /export const agentConfigHintStyle/);
  assert.match(styles, /export const agentConfigPrimaryButtonStyle/);
});
