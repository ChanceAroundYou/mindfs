import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useToastErrors.ts"), "utf8");
const toast = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/shell/Toast.tsx"), "utf8");

assert.match(hook, /errorService\.subscribe/);
assert.match(hook, /error\.severity === "fatal"/);
assert.match(hook, /error\.severity === "error" \? 5000 : 3000/);
assert.match(hook, /setInterval\([\s\S]*?500\)/);
assert.match(hook, /await toast\.error\.retryAction\(\)/);
assert.match(toast, /useToastErrors\(\)/);
assert.doesNotMatch(toast, /services\/net\/error/);

console.log("toast errors hook contract ok");
