// 部署前缀收口回归测试：文本检查“单一规范化真源”约定，并校验派生值。
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
// 1) 单一真源：prefix.ts 导出 DEPLOY_PREFIX / withDeployPrefix
assert.ok(prefixSrc.includes("export const DEPLOY_PREFIX"), "prefix.ts 应导出 DEPLOY_PREFIX");
assert.ok(prefixSrc.includes("export function withDeployPrefix"), "prefix.ts 应导出 withDeployPrefix");

// 2) 前缀形态只有一种：<部署前缀>/assets/。relay 时代的别名前缀（/<前缀>-assets/）
//    已随 relay 整体删除（G-H），main.tsx 里不得再出现它。
assert.ok(!mainSrc.includes("/mindfs-assets/"), "main.tsx 不得出现 relay 别名资源路径");

// 3) vite.config 由 VITE_MIND_FS_BASE 派生；base / SW / HTML 注入均同源
assert.ok(viteSrc.includes("VITE_MIND_FS_BASE"), "vite 应从 VITE_MIND_FS_BASE 派生部署前缀");
assert.ok(viteSrc.includes("normalizeBase"), "vite 应复用 normalizeBase");
assert.ok(!viteSrc.includes("RELAY_ALIAS"), "vite SW 不得再注入 relay 别名变量（G-H）");
assert.ok(!viteSrc.includes("/mindfs-assets/"), "vite SW 不得出现 relay 别名资源路径");
assert.ok(viteSrc.includes("MINDFS_FAVICON_HREF"), "vite 应注入 favicon 前缀占位符");
assert.ok(!viteSrc.includes("MINDFS_MAIN_ASSET_RE"), "vite 不得再注入主包正则（relay 时代的兜底已删，G-H）");

// 4) index.html 使用占位符，而非裸硬编码前缀
assert.ok(htmlSrc.includes("<!--MINDFS_FAVICON_HREF-->"), "index.html favicon 应使用前缀占位符");
assert.ok(!htmlSrc.includes("/mindfs-assets/"), "index.html 不得硬编码 /mindfs-assets/");
assert.ok(!htmlSrc.includes("MINDFS_MAIN_ASSET_RE"), "index.html 不得再有主包检测兜底（G-H）");

// 5) 规范化派生值：默认 /mindfs，根部署退化为空
assert.strictEqual(normalizeBase("/mindfs"), "/mindfs");
assert.strictEqual(normalizeBase(""), "");
assert.strictEqual(normalizeBase("/"), "");
assert.strictEqual(normalizeBase("mindfs/"), "/mindfs");
assert.strictEqual(normalizeBase("/x/y/"), "/x/y");

console.log("deploy-prefix.test.mjs: OK");
