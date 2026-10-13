import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree delegates agent-catalog requests to its transition logic", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const logic = read("../src/components/file/useAgentConfigFlowTransitions.ts");

  assert.doesNotMatch(view, /fetchAgentCatalog|fetchAgents/);
  assert.match(logic, /import \{ fetchAgentCatalog, fetchAgents, type AgentStatus \} from "\.\.\/\.\.\/services\/agents"/);
  assert.doesNotMatch(logic, /const \{[^}]*fetchAgentCatalog[^}]*\} = ctx/);
  assert.doesNotMatch(logic, /const \{[^}]*fetchAgents[^}]*\} = ctx/);
});
