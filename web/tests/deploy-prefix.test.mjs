// 部署前缀收口回归测试：文本检查“单一规范化真源”约定，并校验派生值对齐后端 relay 别名。
// 不依赖构建产物，纯 node/assert。
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const read = (p) => fs.readFileSync(path.join(root, p), "utf8");

const prefixSrc = read("src/services/prefix.ts");
const mainSrc = read("src/main.tsx");
const viteSrc = read("vite.config.ts");
const htmlSrc = read("index.html");

// 规范化规则：与 prefix.ts / vite.config.ts 中同一实现保持一致（测试侧复刻以校验派生值）
function normalizeBase(raw) {
  const v = String(raw || "").trim().replace(/\/+$/, "");
  if (!v || v === "/") return "";
  return v.startsWith("/") ? v : `/${v}`;
}
function relayAlias(prefix) {
  return prefix ? `${prefix}-assets/` : "/assets/";
}

// 1) 单一真源：prefix.ts 导出 DEPLOY_PREFIX / withDeployPrefix / RELAY_ASSETS_PREFIX
assert.ok(prefixSrc.includes("export const DEPLOY_PREFIX"), "prefix.ts 应导出 DEPLOY_PREFIX");
assert.ok(prefixSrc.includes("export function withDeployPrefix"), "prefix.ts 应导出 withDeployPrefix");
assert.ok(prefixSrc.includes("export const RELAY_ASSETS_PREFIX"), "prefix.ts 应导出 RELAY_ASSETS_PREFIX");
assert.ok(prefixSrc.includes("`${DEPLOY_PREFIX}-assets/`"), "RELAY_ASSETS_PREFIX 应由 DEPLOY_PREFIX 派生");
assert.ok(prefixSrc.includes(': "/assets/"'), "空前缀时 relay 别名退化为 /assets/");

// 2) main.tsx 使用 RELAY_ASSETS_PREFIX，不得再硬编码 /mindfs-assets/
assert.ok(mainSrc.includes("RELAY_ASSETS_PREFIX"), "main.tsx 应引用 RELAY_ASSETS_PREFIX");
assert.ok(!mainSrc.includes("/mindfs-assets/"), "main.tsx 不得硬编码 /mindfs-assets/");

// 3) vite.config 由 VITE_MIND_FS_BASE 派生；base / SW / HTML 注入均同源
assert.ok(viteSrc.includes("VITE_MIND_FS_BASE"), "vite 应从 VITE_MIND_FS_BASE 派生部署前缀");
assert.ok(viteSrc.includes("normalizeBase"), "vite 应复用 normalizeBase");
assert.ok(viteSrc.includes("RELAY_ALIAS"), "SW 应注入 RELAY_ALIAS 变量（不得硬编码 /mindfs-assets/）");
assert.ok(!viteSrc.includes("/mindfs-assets/"), "vite SW 不得硬编码 /mindfs-assets/");
assert.ok(viteSrc.includes("MINDFS_FAVICON_HREF"), "vite 应注入 favicon 前缀占位符");
assert.ok(viteSrc.includes("MINDFS_MAIN_ASSET_RE"), "vite 应注入主包正则占位符");

// 4) index.html 使用占位符，而非裸硬编码前缀
assert.ok(htmlSrc.includes("<!--MINDFS_FAVICON_HREF-->"), "index.html favicon 应使用前缀占位符");
assert.ok(htmlSrc.includes("MINDFS_MAIN_ASSET_RE"), "index.html 主包检测应使用占位符");
assert.ok(!htmlSrc.includes("/mindfs-assets/"), "index.html 不得硬编码 /mindfs-assets/");

// 5) 派生值对齐后端约定：默认 /mindfs → relay 别名 /mindfs-assets/
assert.strictEqual(normalizeBase("/mindfs"), "/mindfs");
assert.strictEqual(relayAlias(normalizeBase("/mindfs")), "/mindfs-assets/");
assert.strictEqual(normalizeBase(""), "");
assert.strictEqual(relayAlias(normalizeBase("")), "/assets/");
assert.strictEqual(normalizeBase("/"), "");
assert.strictEqual(normalizeBase("mindfs/"), "/mindfs");
assert.strictEqual(normalizeBase("/x/y/"), "/x/y");

console.log("deploy-prefix.test.mjs: OK");
