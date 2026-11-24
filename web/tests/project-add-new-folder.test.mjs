import assert from "node:assert";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

// 导入项目弹窗里的「新建文件夹」：浏览磁盘时可以就地建目录并继续导入。
//   1) LocalPanel 渲染新建按钮，点了调 onCreateFolder（只在调用方传入时出现，
//      会话改目录那个 bare 复用处不会凭空多出一个按钮）；
//   2) 接线：useProjectDirectoryBrowser 的 handleLocalDirCreate 走 POST /api/local_dirs，
//      并按当前节点打（跨节点不打错机器）；
//   3) 建完按模式收尾：加入本地目录 → 预选新目录（下一步点「添加」就是它）；
//      空白项目 / GitHub / worktree → 进入新目录（「放置于此目录」落在它上面）；
//   4) 3 个 i18n key 两份 locale 齐备。

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("project-add new folder", () => {
  const view = read("src/components/file/ProjectAddPopover.tsx");
  const button = read("src/components/file/LocalNewFolderButton.tsx");
  const hook = read("src/app/useProjectDirectoryBrowser.ts");
  const lifecycle = read("src/app/useProjectLifecycle.tsx");

  // 1) 按钮只在有 handler 时挂载；按钮本体在 presenter 子模块里
  assert.match(view, /onCreateFolder\?: \(\) => void/);
  assert.match(view, /onCreateFolder \? \([\s\S]*?<LocalNewFolderButton/);
  assert.match(button, /export function LocalNewFolderButton/);
  assert.match(button, /onClick=\{onCreateFolder\}/);
  assert.match(button, /t\("projectAdd\.newFolder"\)/);

  // 2) 创建走数据面端点，带当前节点
  assert.match(
    hook,
    /appURL\("\/api\/local_dirs", undefined, targetCreateNodeId\)/,
    "POST 必须打当前节点，不能落到别的机器上",
  );
  assert.match(hook, /method: "POST"/);
  assert.match(hook, /JSON\.stringify\(\{ path: parentPath, name \}\)/);

  // 3) 两种收尾都接上了
  assert.match(hook, /projectAddMode === "local"[\s\S]*?selectedPath: createdPath/);
  assert.match(hook, /loadLocalDirs\(createdPath, targetCreateNodeId\)/);

  // 4) 接线到 LocalPanel
  assert.match(lifecycle, /onCreateFolder=\{\(\) => void handleLocalDirCreate\(\)\}/);
  assert.match(lifecycle, /handleLocalDirCreate/);

  for (const locale of ["zh-CN-workspace.ts", "en-US-workspace.ts"]) {
    const src = read(`src/i18n/locales/${locale}`);
    for (const key of ["projectAdd.newFolder", "projectAdd.newFolderPrompt", "projectAdd.newFolderFailed"]) {
      assert.match(src, new RegExp(`"${key.replace(".", "\\.")}":`), `${key} missing in ${locale}`);
    }
  }

  console.log("project-add-new-folder ok");
});
