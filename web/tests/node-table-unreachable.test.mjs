// 节点表拉不到时，界面必须说「清单可能不是最新」——**有内容的时候更要说**。
//
// 两个真缺口（2026-11-22，Scope: G-PF）：
//   ① `syncNodesFromServer` 失败时会退回缓存 / 只留 local 并**正常返回**，于是
//      `syncNodeTableHealth` 里那句 `markNodeTableReachable()` 照样执行 —— 拉不到
//      被记成「拉到了」。后果不是文案错，是判据错：主节点整个不可达时，界面会拿
//      陈旧清单宣布「所有节点都连不上」，而不是说「这份清单本身就不可信」。
//   ② 提示只在「一条分组都没有」时出现。可陈旧缓存恰恰意味着**有**分组显示在屏幕上，
//      于是最该提醒的时刻（看着一份可能过时的清单）反而一声不响。
//
// 这里用真实调用钉行为，不用源码合同：前两条直接跑 `syncNodeTableHealth`，第三条
// 渲染组件看输出。
import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, test, vi } from "vitest";

const realFetch = globalThis.fetch;

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  globalThis.fetch = realFetch;
  delete globalThis.window;
});

/** 每次 resetModules 之后 nodeHealth 与 nodeRegistry 必须是同一份实例。 */
async function loadModules() {
  const health = await import("../src/services/net/nodeHealth.ts");
  health.resetNodeHealth();
  const nodeHealthIo = await import("../src/app/managedRootNodeHealth.ts");
  return { health, nodeHealthIo };
}

test("节点表拉不到：记成 offline，不能说「拉到了」", async () => {
  globalThis.fetch = async () => {
    throw new Error("connect ECONNREFUSED 192.168.1.6:443");
  };
  const { health, nodeHealthIo } = await loadModules();

  await nodeHealthIo.syncNodeTableHealth();

  assert.equal(
    health.nodeHealthSnapshot().nodeTable?.status,
    "offline",
    "拉取失败被记成成功 → 界面拿陈旧清单宣布「所有节点都连不上」，而不是「清单不可信」",
  );
  assert.match(String(health.nodeHealthSnapshot().nodeTable?.detail || ""), /ECONNREFUSED/);
});

test("节点表拉到了：才记 online（恢复后不再挂着「可能不是最新」）", async () => {
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => [{ id: "local", name: "local", url: "https://home.example/mindfs" }],
  });
  const { health, nodeHealthIo } = await loadModules();

  await nodeHealthIo.syncNodeTableHealth();

  assert.equal(health.nodeHealthSnapshot().nodeTable?.status, "online");
});

test("节点表拉不到时 HTTP 500 也算失败，不能只认抛异常", async () => {
  globalThis.fetch = async () => ({ ok: false, status: 500, json: async () => ({}) });
  const { health, nodeHealthIo } = await loadModules();

  await nodeHealthIo.syncNodeTableHealth();

  assert.equal(health.nodeHealthSnapshot().nodeTable?.status, "offline");
});

// —————————————————— ② 有内容时也要说「清单不可信」 ——————————————————

const renderContent = async (props) => {
  const { FileTreeProjectContent } = await import("../src/components/file/FileTreeProjectContent.tsx");
  return renderToString(
    React.createElement(FileTreeProjectContent, {
      groups: [],
      unreachableNodes: [],
      nodesAvailability: "ok",
      creatingRootName: null,
      t: (key) => key,
      renderEntries: () => null,
      rootId: "root-1",
      ...props,
    }),
  );
};

test("零项目 + 清单不可信：说「清单没拉到」，不说「添一个项目」", async () => {
  const html = await renderContent({ nodesAvailability: "table-unreachable" });
  assert.match(html, /fileTree\.nodesTableUnreachable/);
  assert.doesNotMatch(html, /fileTree\.emptyProjectHint/);
});

test("屏幕上有分组时，清单不可信仍然要提示 —— 陈旧缓存恰恰意味着列表还在", async () => {
  const group = { name: "home", color: "#111", nid: "local", items: [], unreachable: false };
  const html = await renderContent({ groups: [group], nodesAvailability: "table-unreachable" });
  assert.match(html, /home/, "分组本身照常显示");
  assert.match(
    html,
    /fileTree\.nodesTableUnreachable/,
    "有一条分组就一声不响：用户看着的可能是已删除/已改名的节点，却没有任何提示",
  );
});

test("清单可信时不提示（免得每次加载都挂一条噪声）", async () => {
  const group = { name: "home", color: "#111", nid: "local", items: [], unreachable: false };
  const html = await renderContent({ groups: [group], nodesAvailability: "ok" });
  assert.doesNotMatch(html, /fileTree\.nodesTableUnreachable/);
});
