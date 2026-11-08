import { test } from "vitest";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "./source-map-fs.mjs";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

test("release notes begin with a unique version heading for the release tag", () => {
  const notes = readFileSync(resolve(ROOT, "release-notes.md"), "utf8");
  const headings = [...notes.matchAll(/^# MindFS (v\d+\.\d+\.\d+(?:-\d+)?)$/gm)];
  assert.ok(headings.length > 0, "release notes need a version heading");
  assert.equal(notes.split("\n", 1)[0], headings[0][0]);
  assert.equal(new Set(headings.map((heading) => heading[1])).size, headings.length);
});

test("only the root-level generated cmd executable is ignored", () => {
  const rules = readFileSync(resolve(ROOT, ".gitignore"), "utf8");
  assert.ok(rules.split("\n").includes("/cmd"));
  const ignored = (path) => {
    try {
      execFileSync("git", ["check-ignore", "-q", "--no-index", path], { cwd: ROOT });
      return true;
    } catch (error) {
      if (error.status === 1) return false;
      throw error;
    }
  };
  assert.ok(ignored("cmd"));
  assert.ok(ignored("mindfs"));
  assert.ok(!ignored("server/cmd"));
});
