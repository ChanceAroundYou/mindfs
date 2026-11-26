// relay 是**同一台设备上的兜底通道**，不是第二个节点。
//
// 现场症状：relay 接通后共享节点表多出 `relay:pc`，同一台 WSL 边缘在界面里变成两行
// （`pc-wsl` 直连 + `relay:pc`）。设计约定是一台边缘设备 = 一个节点，直连可选、
// 填了就优先，不填则 relay 兜底。
//
// 服务端把 relay 并回同 instanceId 的节点行（relayUrl 辅助字段）；前端这里的职责是：
//   1. url || relayUrl 解析基址（relay-only 节点不能被当成没有地址）
//   2. 遗留 relay:* 行丢弃、同 instanceId 的行合成一行
//   3. 探活学到的 instanceId 只在值变化时写回一次
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import { afterEach, beforeEach, test, vi } from "vitest";

const realFetch = globalThis.fetch;
const putBodies = [];

beforeEach(() => {
  vi.resetModules();
  putBodies.length = 0;
  globalThis.fetch = async (_url, init) => {
    if (init?.method === "PUT") {
      putBodies.push(JSON.parse(String(init.body)));
      return { ok: true, status: 200, json: async () => ({}) };
    }
    return { ok: true, status: 200, json: async () => [] };
  };
});

afterEach(() => {
  globalThis.fetch = realFetch;
});

async function loadRegistry() {
  return import("../src/services/net/nodeRegistry.ts");
}

test("relay-only 节点按 instanceId 并回直连行，遗留 relay:* 行丢弃", async () => {
  const reg = await loadRegistry();
  reg.applyNodesFromServer([
    { id: "node-pc", name: "pc-wsl", url: "https://pc.example/mindfs", instanceId: "pc", remote: true },
    { id: "pc", name: "edge-pc", url: "", relayUrl: "https://home.example/mindfs/relay/edge-pc", instanceId: "pc", remote: true },
    { id: "relay:hasee", name: "hasee", url: "https://home.example/mindfs/relay/edge", remote: true },
    { id: "hasee", name: "edge", url: "", relayUrl: "https://home.example/mindfs/relay/edge", instanceId: "hasee", remote: true },
  ]);

  const nodes = reg.getNodes().filter((n) => !reg.isSelfNode(n.id));
  assert.equal(nodes.length, 2, `一台设备只能有一行：${JSON.stringify(nodes)}`);

  const pc = nodes.find((n) => n.instanceId === "pc");
  assert.ok(pc, "pc 行必须存在");
  assert.equal(pc.id, "node-pc", "应保留直连行（沿用它的 id）");
  assert.equal(pc.url, "https://pc.example/mindfs", "直连 URL 不能被 relay 覆盖");
  assert.equal(pc.relayUrl, "https://home.example/mindfs/relay/edge-pc", "relay 必须作为兜底字段并进同一行");
  assert.equal(reg.nodeRouteBase(pc), "https://pc.example/mindfs", "有直连时优先直连");

  const hasee = nodes.find((n) => n.instanceId === "hasee");
  assert.ok(hasee, "relay-only 设备必须保留一行");
  assert.equal(hasee.url, "", "relay-only 行没有直连地址");
  assert.equal(reg.nodeRouteBase(hasee), "https://home.example/mindfs/relay/edge", "没有直连时用 relay 兜底");
  assert.ok(!nodes.some((n) => n.id.startsWith("relay:")), "不得再有 relay:* 伪节点");
});

test("探活学到的 instanceId 只在变化时写回一次", async () => {
  const reg = await loadRegistry();
  reg.applyNodesFromServer([
    { id: "node-pc", name: "pc-wsl", url: "https://pc.example/mindfs" },
  ]);

  assert.equal(reg.learnNodeInstanceId("node-pc", "pc"), true, "首次学到身份必须写回");
  await vi.waitFor(() => assert.equal(putBodies.length, 1));
  assert.equal(putBodies[0].find((n) => n.id === "node-pc").instanceId, "pc");

  assert.equal(reg.learnNodeInstanceId("node-pc", "pc"), false, "值没变不得重复写整表");
  assert.equal(reg.learnNodeInstanceId("node-pc", ""), false, "空身份不得写入");
  assert.equal(reg.learnNodeInstanceId("missing", "pc"), false, "不存在的节点不得写");
});

test("启动时在拉完节点表后自动学设备身份", () => {
  // 只在节点面板打开时才学的话，用户不点「管理节点」就永远看到两行；
  // 学习必须挂在启动同步完成后（nodeIdentitySync 每台机器只探一次）。
  const appLayer = fs.readFileSync(new URL("../src/app/useAppStateLayer.ts", import.meta.url), "utf8");
  assert.match(
    appLayer,
    /syncNodesFromServer\(\)\s*\.then\(\(\) => learnNodeIdentitiesOnce\(\)\)/,
    "启动同步完成后必须触发 learnNodeIdentitiesOnce",
  );
});

test("updateNode 允许在有 relay 兜底时清空直连，但不能把节点清成没有地址", async () => {
  const reg = await loadRegistry();
  reg.applyNodesFromServer([
    { id: "a", name: "a", url: "https://a.example", relayUrl: "https://home.example/relay/a", instanceId: "a" },
    { id: "b", name: "b", url: "https://b.example" },
  ]);

  const a = await reg.updateNode("a", { url: "" });
  assert.equal(a.url, "", "有 relay 兜底时允许清空直连");
  assert.equal(reg.nodeRouteBase(a), "https://home.example/relay/a");

  const b = await reg.updateNode("b", { url: "" });
  assert.equal(b.url, "https://b.example", "没有兜底通道时清空 URL 等于删节点，必须保留原值");
});
