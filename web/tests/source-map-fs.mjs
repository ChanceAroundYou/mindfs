// Vitest adapter for logical source reads. Bare "fs" accesses the platform module.
import * as fs from "fs";
import path from "node:path";
import { MODULES, WEB_ROOT, toLogical, rawReadFileSync } from "./source-map.mjs";

export const readFileSync = (target, options) => {
  const logical = toLogical(target);
  const files = logical && MODULES[logical];
  return files
    ? files.map((file) => rawReadFileSync(path.join(WEB_ROOT, file), options)).join("\n")
    : fs.readFileSync(target, options);
};

export const {
  closeSync,
  existsSync,
  openSync,
  readdirSync,
  statSync,
} = fs;

export default { ...fs, readFileSync };
