// Naming rules are universal: no path-based grandfather list.
import { test } from "vitest";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "./source-map-fs.mjs";
import { join, basename, extname } from "node:path";
import { fileURLToPath } from "node:url";
import { transformSync } from "esbuild";
import ts from "typescript";
import { rawReadFileSync } from "./source-map.mjs";

const SRC = join(fileURLToPath(import.meta.url), "..", "..", "src");

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const file = join(dir, entry);
    if (statSync(file).isDirectory()) {
      if (entry !== "node_modules") walk(file, out);
    } else if (file.endsWith(".ts") || file.endsWith(".tsx")) out.push(file);
  }
  return out;
}

function tsHasJsx(source) {
  try {
    transformSync(source, { loader: "ts" });
    return false;
  } catch (error) {
    return /expected ">"|unterminated|JSX|jsx/i.test(String(error?.message || error));
  }
}

function exportedDeclarations(file, source) {
  const parsed = ts.createSourceFile(
    file,
    source,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const declarations = [];
  for (const statement of parsed.statements) {
    const modifiers = ts.canHaveModifiers(statement) ? ts.getModifiers(statement) : undefined;
    if (!modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) continue;
    if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        declarations.push({ name: declaration.name.getText(parsed), node: declaration, typeOnly: false });
      }
    } else if (statement.name) {
      declarations.push({
        name: statement.name.text,
        node: statement,
        typeOnly: ts.isInterfaceDeclaration(statement) || ts.isTypeAliasDeclaration(statement),
      });
    }
  }
  return declarations;
}

function containsJsx(node) {
  let found = false;
  function visit(current) {
    if (found) return;
    if (ts.isJsxElement(current) || ts.isJsxSelfClosingElement(current) || ts.isJsxFragment(current)) {
      found = true;
      return;
    }
    ts.forEachChild(current, visit);
  }
  visit(node);
  return found;
}

function isHookDeclaration(declaration) {
  return /^use[A-Z]/.test(declaration.name)
    && (ts.isFunctionDeclaration(declaration.node)
      || (ts.isVariableDeclaration(declaration.node) && declaration.node.initializer));
}

test(".ts files contain no JSX", () => {
  const violations = walk(SRC).filter((file) => file.endsWith(".ts") && !file.endsWith(".d.ts"))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: contains JSX`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test(".tsx files contain JSX", () => {
  const violations = walk(SRC).filter((file) => file.endsWith(".tsx"))
    .filter((file) => {
      const parsed = ts.createSourceFile(file, rawReadFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
      return !containsJsx(parsed);
    })
    .map((file) => `${file}: .tsx files must contain JSX`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("Utils files contain no JSX", () => {
  const violations = walk(SRC).filter((file) => /Utils\.ts$/.test(file))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: *Utils.ts must contain pure logic`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("JSX helpers use Helpers.tsx", () => {
  const violations = walk(SRC).filter((file) => /Utils\.tsx$/.test(file))
    .filter((file) => tsHasJsx(rawReadFileSync(file, "utf8")))
    .map((file) => `${file}: JSX helpers must use *Helpers.tsx`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("component domain types use a domain-specific name", () => {
  const violations = walk(SRC).filter((file) => /components\/[^/]+\/types\.ts$/.test(file.slice(SRC.length + 1)))
    .map((file) => `${file}: use a domain-specific *Types.ts name`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("style modules use Styles.ts and contain no JSX", () => {
  const violations = walk(SRC).filter((file) => /Styles\.tsx$/.test(basename(file)))
    .map((file) => `${file}: style modules must use *Styles.ts`);
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("standalone component entries use the exported component name", () => {
  const violations = [];
  for (const file of walk(SRC).filter((name) => name.endsWith(".tsx") && name.includes("/components/"))) {
    const declarations = exportedDeclarations(file, rawReadFileSync(file, "utf8"));
    const runtime = declarations.filter((declaration) => !declaration.typeOnly);
    const components = runtime.filter((declaration) => /^[A-Z]/.test(declaration.name) && containsJsx(declaration.node));
    // Multiple exported components or other runtime exports identify a responsibility module.
    if (components.length !== 1 || runtime.length !== 1) continue;
    const expected = basename(file, ".tsx");
    if (components[0].name !== expected) {
      violations.push(`${file}: standalone component ${components[0].name} must use ${components[0].name}.tsx`);
    }
  }
  assert.deepEqual(violations, [], violations.join("\n"));
});

test("standalone hook entries use the exported hook name", () => {
  const violations = [];
  for (const file of walk(SRC).filter((name) => name.includes("/hooks/") && /\.tsx?$/.test(name))) {
    const declarations = exportedDeclarations(file, rawReadFileSync(file, "utf8"));
    const runtime = declarations.filter((declaration) => !declaration.typeOnly);
    const hooks = runtime.filter(isHookDeclaration);
    if (hooks.length !== 1 || runtime.length !== 1) continue;
    const expected = basename(file, extname(file));
    if (hooks[0].name !== expected) {
      violations.push(`${file}: standalone hook ${hooks[0].name} must use ${hooks[0].name}.ts`);
    }
  }
  assert.deepEqual(violations, [], violations.join("\n"));
});
