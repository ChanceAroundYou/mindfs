import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("account-panel-actions", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const hook = read("src/components/account/useAccountPanel.ts");
const view = read("src/components/account/AccountPanel.tsx");

assert.match(hook, /confirmDialog\(\{/);
assert.match(hook, /deleteAccount\(account\.id\)/);
assert.match(hook, /setPrimaryAccount\(accountId\)/);
assert.match(hook, /logout\(\);\s*window\.location\.reload\(\)/);
assert.match(hook, /await refresh\(\)/, "successful account mutations refresh the account list");
assert.match(view, /confirmDeleteAccount\(account\)/);
assert.match(view, /makePrimaryAccount\(account\.id\)/);
assert.match(view, /signOut\(\)/);
assert.doesNotMatch(view, /services\/(accounts|dialog|net\/authGate)/);

console.log("account panel actions contract ok");
});
