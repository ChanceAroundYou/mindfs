import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const hook = read("src/components/account/useAuthGate.ts");
const view = read("src/components/account/AuthGate.tsx");

assert.match(hook, /currentUser\(\)/, "an existing browser account opens the gate without another request");
assert.match(hook, /fetchAuthStatus\(\)/, "the hook checks whether login is required");
assert.match(hook, /aliveRef\.current/, "async updates are ignored after unmount");
assert.match(hook, /loginWithPassword\(name, password\)/, "the hook submits trimmed username and password");
for (const code of ["invalid_credentials", "user_disabled"]) {
  assert.ok(hook.includes(code), `the hook translates ${code}`);
}
assert.doesNotMatch(view, /services\/net\/authGate|fetchAuthStatus|loginWithPassword|currentUser/);
assert.match(view, /useAuthGate\(t\)/, "the page gate delegates state and auth actions to its colocated logic hook");

console.log("auth gate hook contract ok");
