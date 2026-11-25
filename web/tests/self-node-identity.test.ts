// 本机自报身份：前端不再用伪节点 local 表示「当前这台机器」。
//
// 钉住三件事：
//   1. 身份未知时 id 退回 local（兼容读，行为与改造前一致）；
//   2. node-info 拉到 id/name 后，作用域键与节点表都跟着换；
//   3. 拉取失败不清空已知身份（离线重启不能把本机认成陌生节点）。
import assert from "node:assert/strict";
import { beforeEach, test, vi } from "vitest";

class MemoryStorage implements Storage {
  private map = new Map<string, string>();
  get length(): number {
    return this.map.size;
  }
  clear(): void {
    this.map.clear();
  }
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null;
  }
  key(index: number): string | null {
    return Array.from(this.map.keys())[index] ?? null;
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value);
  }
}

let storage: MemoryStorage;

beforeEach(() => {
  vi.resetModules();
  storage = new MemoryStorage();
  // node 环境没有 window：identity 模块在 import 时读缓存，所以先装好再动态 import。
  (globalThis as any).window = {
    localStorage: storage,
    location: { origin: "http://127.0.0.1:7331", href: "http://127.0.0.1:7331/", pathname: "/" },
  };
});

async function loadSelfNode() {
  return import("../src/services/net/selfNode.ts");
}

test("拿到 node-info 之前退回旧 id local", async () => {
  const self = await loadSelfNode();
  assert.equal(self.getSelfNodeId(), "local");
  assert.equal(self.isSelfNode("local"), true, "旧 id 一律当本机");
  assert.equal(self.isSelfNode("pc-win"), false);
});

test("node-info 的 id/name 成为本机身份，并把旧作用域键迁走", async () => {
  storage.setItem("mindfs_active_node_id", "local");
  storage.setItem("mindfs-workspace-collapsed", JSON.stringify(["local::mindfs:/src"]));
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({ id: "home", name: "主控" }),
  }));
  (globalThis as any).fetch = fetchMock;

  const self = await loadSelfNode();
  const identity = await self.initSelfNodeIdentity();

  assert.deepEqual(identity, { id: "home", name: "主控" });
  assert.equal(self.getSelfNodeId(), "home");
  assert.equal(self.getSelfNodeName(), "主控");
  assert.equal(self.isSelfNode("home"), true);
  assert.equal(self.isSelfNode("pc-win"), false);
  assert.equal(storage.getItem("mindfs_active_node_id"), "home");
  assert.deepEqual(JSON.parse(storage.getItem("mindfs-workspace-collapsed")!), ["home::mindfs:/src"]);
  // 缓存下来：下次启动即使 node-info 失败也知道自己是谁
  assert.deepEqual(JSON.parse(storage.getItem("mindfs_self_node")!), { id: "home", name: "主控" });
});

test("node-info 返回空 id 或 local 时不改写身份", async () => {
  (globalThis as any).fetch = vi.fn(async () => ({ ok: true, json: async () => ({ id: "local", name: "local" }) }));
  const self = await loadSelfNode();
  assert.equal(await self.initSelfNodeIdentity(), null);
  assert.equal(self.getSelfNodeId(), "local");
});

test("拉取失败时保留缓存身份，并允许下次重试", async () => {
  storage.setItem("mindfs_self_node", JSON.stringify({ id: "pc-win", name: "书房 PC" }));
  let fail = true;
  (globalThis as any).fetch = vi.fn(async () => {
    if (fail) throw new Error("offline");
    return { ok: true, json: async () => ({ id: "pc-win", name: "书房 PC" }) };
  });

  const self = await loadSelfNode();
  assert.equal(self.getSelfNodeId(), "pc-win", "缓存里的身份先顶上");
  assert.deepEqual(await self.initSelfNodeIdentity(), { id: "pc-win", name: "书房 PC" });

  // 失败后 promise 被清掉：下一次调用会重新请求
  fail = false;
  (globalThis as any).fetch = vi.fn(async () => ({ ok: true, json: async () => ({ id: "pc-win", name: "书房 PC" }) }));
  await self.initSelfNodeIdentity();
  assert.equal(((globalThis as any).fetch as any).mock.calls.length, 1);
});
