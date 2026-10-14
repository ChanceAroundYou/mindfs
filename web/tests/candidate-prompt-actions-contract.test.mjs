import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useCandidatePromptActions.ts"), "utf8");
const component = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/action/CandidateDropdown.tsx"), "utf8");

assert.match(hook, /import \{ deletePrompt, savePrompt \} from "\.\.\/services\/prompts"/);
assert.match(hook, /await savePrompt\(text\)/);
assert.match(hook, /onPromptSaved\(\)/);
assert.match(hook, /addErrorMessage/);
assert.match(hook, /await deletePrompt\(text\)/);
assert.match(hook, /onPromptDeleted\(text\)/);
assert.match(hook, /deleteErrorMessage/);
assert.match(component, /useCandidatePromptActions\(/);
assert.doesNotMatch(component, /services\/prompts/);

console.log("candidate prompt actions contract ok");
