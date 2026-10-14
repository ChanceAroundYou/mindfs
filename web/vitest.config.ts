import { defineConfig } from "vitest/config";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { redirectSingleFileModule } from "./tests/source-map.mjs";

const webRoot = path.dirname(fileURLToPath(import.meta.url));

const movedSourceModules = {
  name: "resolve-moved-source-modules-for-tests",
  enforce: "pre" as const,
  resolveId(source: string, importer?: string) {
    if (!importer || (!source.startsWith("./") && !source.startsWith("../"))) return null;
    const suffix = path.extname(source);
    if (!/^\.tsx?$/.test(suffix)) return null;
    const logicalPath = path.resolve(path.dirname(importer), source.slice(0, -suffix.length));
    const physicalPath = redirectSingleFileModule(logicalPath);
    if (!physicalPath || !physicalPath.startsWith(webRoot + path.sep)) return null;
    return physicalPath + suffix;
  },
};

export default defineConfig({
  plugins: [movedSourceModules],
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts", "tests/**/*.test.mjs"],
    clearMocks: false,
  },
});
