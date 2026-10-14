import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("agent-icon-hook", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const view = read("src/components/agent/AgentIcon.tsx");
const hook = read("src/components/agent/useCachedAgentIcon.ts");

assert.doesNotMatch(view, /services\//, "AgentIcon view should not depend on services");
assert.match(view, /useCachedAgentIcon\(icon\?\.src\)/);
assert.match(hook, /appPath\(`\/assets\/agents\/\$\{fileName\}`\)/, "asset URLs keep deployment-prefix routing");
assert.match(hook, /loadIconOnce\(url\)/, "SVG fetch caching stays in logic");
assert.match(hook, /if \(!cancelled\) setSrc\(url\)/, "failed SVG loads fall back to the asset URL");

console.log("agent icon hook contract ok");
});
