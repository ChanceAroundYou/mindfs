import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const view = read("src/components/session/SessionViewerTimeline.tsx");
const hook = read("src/components/session/useSessionViewerTimelineActions.ts");

assert.doesNotMatch(view, /services\/(session|prompts)/);
assert.match(view, /useSessionViewerTimelineActions\(\)/);
assert.match(hook, /import \{ isPersistedSeq \} from "\.\.\/\.\.\/services\/session"/);
assert.match(hook, /import \{ savePrompt \} from "\.\.\/\.\.\/services\/prompts"/);
assert.match(hook, /return \{ isPersistedSeq, savePrompt \}/);

console.log("session viewer timeline actions contract ok");
