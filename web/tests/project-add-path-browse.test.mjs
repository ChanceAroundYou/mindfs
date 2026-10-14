import assert from "node:assert";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

test("project-add-path-browse", async () => {
// 添加项目弹窗的路径浏览回归：必须能上溯到 / 及以上（/mnt 等），且根目录不能丢失可点锚点。
// 纯源码文本断言，不依赖构建产物。





const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const src = fs.readFileSync(path.join(root, "src/components/file/ProjectAddPopover.tsx"), "utf8");
const breadcrumbSrc = fs.readFileSync(path.join(root, "src/components/file/PathBreadcrumb.tsx"), "utf8");
const browserHook = fs.readFileSync(path.join(root, "src/app/useProjectDirectoryBrowser.ts"), "utf8");

// 1) LocalPanel 有「上一级」按钮，走 onLocalNavigate(parent)
const localPanel = src.slice(src.indexOf("function LocalPanel("), src.indexOf("function GitHubPanel("));
assert.ok(localPanel.includes('t("projectAdd.goUp")'), "LocalPanel 应渲染上一级按钮");
assert.ok(
  localPanel.includes("onLocalNavigate(localState.parent"),
  "上一级按钮应导航到 localState.parent",
);

// 2) 根路径（segments 为空）仍需渲染可点的 "/" 锚点，否则停在 / 无法回退
const breadcrumb = breadcrumbSrc.slice(
  breadcrumbSrc.indexOf("if (segments.length === 0)"),
  breadcrumbSrc.indexOf("const volumeItems"),
);
assert.ok(breadcrumb.includes('onNavigate("/")'), '根路径分支应渲染可点的 "/" 锚点');

assert.match(browserHook, /appURL\("\/api\/local_dirs", params, targetNodeId\)/);
assert.match(browserHook, /appPath\("\/api\/dirs", targetAddNodeId\)/);
assert.match(browserHook, /projectAddMode === "local" && target\.is_added_root/);
assert.match(browserHook, /await refreshManagedRoots\(\)[\s\S]*?openManagedDir\([\s\S]*?loadLocalDirs\(localPath\)/);

console.log("project-add-path-browse ok");
});
