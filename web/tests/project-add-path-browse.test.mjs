// 添加项目弹窗的路径浏览回归：必须能上溯到 / 及以上（/mnt 等），且根目录不能丢失可点锚点。
// 纯源码文本断言，不依赖构建产物。
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const src = fs.readFileSync(path.join(root, "src/components/ProjectAddPopover.tsx"), "utf8");

// 1) LocalPanel 有「上一级」按钮，走 onLocalNavigate(parent)
const localPanel = src.slice(src.indexOf("function LocalPanel("), src.indexOf("function GitHubPanel("));
assert.ok(localPanel.includes('t("projectAdd.goUp")'), "LocalPanel 应渲染上一级按钮");
assert.ok(
  localPanel.includes("onLocalNavigate(localState.parent"),
  "上一级按钮应导航到 localState.parent",
);

// 2) 根路径（segments 为空）仍需渲染可点的 "/" 锚点，否则停在 / 无法回退
const breadcrumb = src.slice(src.indexOf("function PathBreadcrumb("), src.indexOf("function ModeItem("));
const rootBranch = breadcrumb.slice(
  breadcrumb.indexOf("if (segments.length === 0)"),
  breadcrumb.indexOf("const volumeItems"),
);
assert.ok(rootBranch.includes('onNavigate("/")'), '根路径分支应渲染可点的 "/" 锚点');

console.log("project-add-path-browse ok");
