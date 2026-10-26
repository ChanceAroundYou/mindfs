import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 「全部」模板页签下点新建器的模板项，永远弹不出新建面板（2026-10-26 用户实测）。
//
// 根因不在菜单本身，在 ref 链路断了：useTaskTemplates 造了
// taskTemplateActionMenuRef / taskCreateTemplateMenuRef，一路透传到工具栏，
// 但两个叶子组件根本不接受 ref —— current 恒为 null，点外关闭的 mousedown
// 监听把每次点击都判成「点在外面」，菜单在 click 到达前就卸载了。
// 同一个缺陷静默打断了 ⋯ 菜单里的新建/编辑/删除模板。
//
// 这几条断言钉住的是**接线**：ref 必须真的挂到菜单的根 div 上，
// 否则事件顺序（mousedown 先于 click）会重新把这个 bug 带回来。

test("task-template-menu-outside-click", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const createMenu = read("src/components/task/TaskCreateTemplateMenu.tsx");
const actionMenu = read("src/components/task/TaskTemplateActionMenu.tsx");
const toolbar = read("src/components/task/TaskBoardToolbar.tsx");
const hook = read("src/app/useTaskTemplates.ts");

// 引用必须从同一处产生，且被点外关闭的监听消费 —— 否则「接线」无从谈起。
assert.match(hook, /useRef<HTMLDivElement \| null>\(null\)/, "useTaskTemplates must own the menu refs");
assert.match(hook, /contains\(/, "the outside-click listener must use the refs to decide");

for (const [name, source, refName] of [
  ["TaskCreateTemplateMenu", createMenu, "taskCreateTemplateMenuRef"],
  ["TaskTemplateActionMenu", actionMenu, "taskTemplateActionMenuRef"],
]) {
  assert.match(
    source,
    /ref\?:\s*React\.Ref<HTMLDivElement>/,
    `${name} must accept a ref prop (React 19: plain prop, no forwardRef needed)`,
  );
  // 必须挂在**外层定位容器**上，而不是某个内层节点：监听拿它 contains() 判断
  // 点击是否落在菜单里，挂错层会让弹层内部的点击被当成「点在外面」。
  assert.match(
    source,
    /<div ref=\{ref\} style=\{\{ position: "relative", flexShrink: 0 \}\}>/,
    `${name} must attach the ref to its outermost relative container`,
  );
  assert.ok(
    toolbar.includes(`ref={${refName}}`),
    `TaskBoardToolbar must pass ref={${refName}} down to ${name}`,
  );
}
});
