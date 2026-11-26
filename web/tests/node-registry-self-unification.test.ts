// 节点表：本机行统一 + 顺序持久化（Scope: G-SA）。
//
// 钉住四件事：
//   1. 边缘上共享的 pc 行（随机 uuid + 学到的 instanceId）与合成本机行合并成一条，
//      保留旧直连网址与主控在前的顺序；
//   2. 合成本机行不写回服务端（否则「同名不同 id」会让整表 PUT 被拒），
//      学到的 instanceId 照常落库；
//   3. 首行固定：首行不能移动，其它行上移/下移按数组顺序整表写回；
//   4. 本机连接的寻址走当前设备地址，而不是表里那条直连网址。
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
let fetchMock: ReturnType<typeof vi.fn>;

const TABLE = [
  { id: "home", name: "home", url: "https://home.xiaokubao.space/mindfs", color: "#3b82f6" },
  { id: "wsl", name: "pc-wsl", url: "https://pc.xiaokubao.space/mindfs", color: "#f59e0b" },
  { id: "fn", name: "fn", url: "https://fn.xiaokubao.space/mindfs", color: "#7c6bd6" },
  { id: "685a", name: "pc", url: "https://pc.xiaokubao.space/mindfs-win", color: "#10b981", remote: true },
];

beforeEach(() => {
  vi.resetModules();
  vi.restoreAllMocks();
  storage = new MemoryStorage();
  (globalThis as any).CustomEvent = class CustomEvent {
    constructor(public type: string) {}
  };
  (globalThis as any).window = {
    localStorage: storage,
    location: { origin: "http://127.0.0.1:7332", href: "http://127.0.0.1:7332/mindfs/", pathname: "/mindfs/" },
    dispatchEvent: () => true,
    addEventListener: () => {},
    removeEventListener: () => {},
  };
  fetchMock = vi.fn(async () => ({ ok: true, json: async () => [] }));
  (globalThis as any).fetch = fetchMock;
});

async function loadEdge() {
  const self = await import("../src/shared/selfNodeStore");
  self.setSelfNodeIdentity("pc", "pc");
  const reg = await import("../src/services/net/nodeRegistry");
  return reg;
}

function lastPutBody(): any[] {
  const put = [...fetchMock.mock.calls].reverse().find(([, init]) => (init as any)?.method === "PUT");
  assert.ok(put, "expected a PUT /api/nodes");
  return JSON.parse(String((put as any)[1].body));
}

test("共享 pc 行学到 instanceId 后与本机合成行合并成一条，保留旧直连网址", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer(TABLE.map((n) => ({ ...n })));

  // 还没学到 instanceId：临时多出一行本机合成行，但它在最前一行之后、不顶替主控。
  const before = reg.getNodes();
  assert.equal(before[0].id, "home", "主控必须留在第一行");
  assert.equal(before.filter((n) => n.name === "pc").length, 2);

  reg.applyNodesFromServer(TABLE.map((n) => (n.id === "685a" ? { ...n, instanceId: "pc" } : { ...n })));
  const after = reg.getNodes();
  assert.deepEqual(after.map((n) => n.id), ["home", "wsl", "fn", "685a"]);
  const pc = after.find((n) => n.id === "685a")!;
  assert.equal(pc.url, "https://pc.xiaokubao.space/mindfs-win", "统一后的 pc 行保留旧直连网址");
  assert.equal(pc.color, "#10b981");
  assert.equal(pc.remote, false, "本机不是远程节点");
  assert.equal(pc.instanceId, "pc");
});

test("合成本机行不写回服务端，学到的 instanceId 正常落库", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer(TABLE.map((n) => ({ ...n })));
  assert.equal(reg.learnNodeInstanceId("685a", "pc"), true);
  // learn 走 writeQueue 异步写回；等一个 tick 让 PUT 真正发出。
  await new Promise((resolve) => setTimeout(resolve, 0));

  const body = lastPutBody();
  assert.equal(body.some((n) => n.id === "pc"), false, "合成的本机行不得写回服务端");
  assert.equal(body.some((n) => String(n.url).includes("127.0.0.1")), false);
  assert.equal(body.find((n) => n.id === "685a")?.instanceId, "pc");
});

test("空表时合成本机行要落库（首次启动的种子行）", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer([]);
  await reg.setNodes(reg.getNodes());
  const body = lastPutBody();
  assert.deepEqual(body.map((n) => n.id), ["pc"]);
});

test("首行固定，其它行上移下移按数组顺序整表写回", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer(TABLE.map((n) => (n.id === "685a" ? { ...n, instanceId: "pc" } : { ...n })));

  await reg.moveNode("home", "down");
  assert.deepEqual(reg.getNodes().map((n) => n.id), ["home", "wsl", "fn", "685a"], "首行不能被移走");
  await reg.moveNode("wsl", "up");
  assert.deepEqual(reg.getNodes().map((n) => n.id), ["home", "wsl", "fn", "685a"], "第二行不能挤到首行前面");

  await reg.moveNode("fn", "up");
  assert.deepEqual(reg.getNodes().map((n) => n.id), ["home", "fn", "wsl", "685a"]);
  assert.deepEqual(lastPutBody().map((n) => n.id), ["home", "fn", "wsl", "685a"], "顺序按数组下标写回");
});

test("旧 active id（本机自报 id）迁到合并后那条行的 id", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer(TABLE.map((n) => (n.id === "685a" ? { ...n, instanceId: "pc" } : { ...n })));
  storage.setItem("mindfs_active_node_id", "pc");
  assert.equal(reg.getActiveNodeId(), "685a");
  assert.equal(storage.getItem("mindfs_active_node_id"), "685a");
});

test("本机连接的寻址走当前设备，共享行里的直连网址只用于展示", async () => {
  const reg = await loadEdge();
  reg.applyNodesFromServer(TABLE.map((n) => (n.id === "685a" ? { ...n, instanceId: "pc" } : { ...n })));
  const runtime = await import("../src/services/platform/runtime");
  assert.equal(runtime.getApiBaseURL("685a"), "http://127.0.0.1:7332/mindfs");
  assert.equal(runtime.getApiBaseURL("fn"), "https://fn.xiaokubao.space/mindfs");
});