// 节点不可达的基线：把「已经做对的」和「仍然缺的」分开钉住。
//
// 2026-11-21 离线优先方案（Scope: G-PF）要求：主节点或某个 worker 不可达时，
// 界面必须显式说「不可达」，而不是把该项目/会话从列表里静默抹掉。
//
// 取证结论（2026-11-21）：**单节点失败**这一半已经做对了 —— 失败会弹提示，
// 且失败节点的项目条目会被保留（见 `useManagedRoots.ts:297-338` 的注释，
// 那是「PC 的项目凭空消失」修出来的）。本文件把它们钉成不许回退的锚点。
// 仍然缺的是另外两处：节点表同步失败被静默吞掉、零项目分支不区分
// 「真的空」与「拉不到」。P6 会改写这两条，届时先红后绿。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const managedRoots = read("src/app/useManagedRoots.ts");

test("已做对：单节点失败会显式提示，不是静默空数组", () => {
  // 失败分支仍先记一笔（内层），真正的可见性在 refreshManagedRoots 里。
  assert.match(
    managedRoots,
    /catch \(err\) \{\s*nodeFailures\.push\(\{ id: String\(n\.id\), name: String\(n\.name \|\| n\.id\) \}\);\s*return \[\] as ManagedRootPayload\[\];/,
    "单节点失败分支的形状变了：先红再改本断言之前，确认提示链路还在",
  );
  assert.match(
    managedRoots,
    /for \(const failed of nodeLoadFailuresRef\.current\) \{\s*notifyNodeLoadFailedRef\.current\(failed\);/,
    "失败节点不再弹提示：用户会把「连不上」看成「没有项目」",
  );
});

test("已做对：失败节点的项目条目会被保留，不能整表替换", () => {
  assert.match(
    managedRoots,
    /const failedIds = new Set\(nodeLoadFailuresRef\.current\.map\(\(f\) => String\(f\.id \|\| ""\)\)\);/,
    "保留失败节点条目的判据不见了",
  );
  assert.match(
    managedRoots,
    /if \(nid && failedIds\.has\(nid\)\) preserved\[key\] = entry as ManagedRootPayload;/,
    "失败节点的项目不再保留：工作台/文件树里会出现「项目凭空消失」",
  );
});

test("仍缺：节点表同步失败被静默吞掉，首屏只剩本机节点", () => {
  assert.match(
    managedRoots,
    /await syncNodesFromServer\(\)\.catch\(\(\) => \{\}\);/,
    "节点表同步失败分支变了：P6 要记录并展示「节点表不可达」",
  );
});

test("仍缺：没有任何项目时直接清空会话与主区，不区分「真空」与「拉不到」", () => {
  assert.match(
    managedRoots,
    /if \(nextRootIds\.length === 0\) \{[\s\S]*?setMainEntries\(\[\]\);[\s\S]*?setSessions\(\[\]\);/,
    "空项目分支被改写了：P6 需要在这里先判断「是真空还是不可达」",
  );
});
