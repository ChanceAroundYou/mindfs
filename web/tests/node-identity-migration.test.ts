// 身份迁移：旧伪节点 id `local` → /api/node-info 自报的 id。
//
// 这是本轮数据风险最高的一步：作用域键同时用 "::" 与 ":" 两种分隔，
// 会话 key 自己也带 "::"，所以只能逐段替换；改错了症状是「旧数据全都消失」。
import assert from "node:assert/strict";
import { test } from "vitest";
import {
  LEGACY_SELF_NODE_ID,
  migrateLegacyLocalNodeId,
  rewriteNodeSegment,
} from "../src/shared/nodeIdentityMigration.ts";

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

test("只替换节点段，不碰路径与会话 key 里的 local", () => {
  assert.equal(rewriteNodeSegment("local::mindfs::sess", "home"), "home::mindfs::sess");
  assert.equal(rewriteNodeSegment("local::mindfs:/src", "home"), "home::mindfs:/src");
  assert.equal(rewriteNodeSegment("vp-progress:local::mindfs:src/a.ts", "home"), "vp-progress:home::mindfs:src/a.ts");
  assert.equal(rewriteNodeSegment("local::mindfs::v2::local::x", "home"), "home::mindfs::v2::local::x", "只改节点段，键尾的 local 不动");
  assert.equal(
    rewriteNodeSegment("mindfs.git.history.files:local%3A%3Amindfs:abc", "home"),
    "mindfs.git.history.files:home%3A%3Amindfs:abc",
    "git 缓存把作用域键 encodeURIComponent 了，节点段同样要换",
  );
  assert.equal(rewriteNodeSegment("/home/x/local/file.ts", "home"), "/home/x/local/file.ts");
  assert.equal(rewriteNodeSegment("mindfs-local-setting", "home"), "mindfs-local-setting");
});

test("迁移改写作用域键、JSON 结构引用与活动节点 id，且幂等", () => {
  const storage = new MemoryStorage();
  storage.setItem("mindfs_active_node_id", "local");
  storage.setItem("mindfs-workspace-collapsed", JSON.stringify(["local::mindfs:/src", "pc::docs"]));
  storage.setItem(
    "mindfs-task-create-worktree-pref",
    JSON.stringify({ "local::mindfs": { createWorktree: true }, "pc::docs": { createWorktree: false } }),
  );
  storage.setItem("vp-progress:local::mindfs:src/a.ts", JSON.stringify({ q: "x" }));
  storage.setItem("mindfs-last-root-node::chance", "local");
  storage.setItem("mindfs-draft", JSON.stringify({ text: "local:: 不是键也不是数组元素", at: 1 }));

  const changed = migrateLegacyLocalNodeId("home", storage);
  assert.ok(changed > 0);

  assert.equal(storage.getItem("mindfs_active_node_id"), "home");
  assert.equal(storage.getItem("mindfs-last-root-node::chance"), "home");
  assert.deepEqual(JSON.parse(storage.getItem("mindfs-workspace-collapsed")!), ["home::mindfs:/src", "pc::docs"]);
  assert.deepEqual(Object.keys(JSON.parse(storage.getItem("mindfs-task-create-worktree-pref")!)), [
    "home::mindfs",
    "pc::docs",
  ]);
  assert.equal(storage.getItem("vp-progress:local::mindfs:src/a.ts"), null);
  assert.deepEqual(JSON.parse(storage.getItem("vp-progress:home::mindfs:src/a.ts")!), { q: "x" });
  // 普通字符串值（可能是用户正文）不参与迁移
  assert.deepEqual(JSON.parse(storage.getItem("mindfs-draft")!), { text: "local:: 不是键也不是数组元素", at: 1 });

  // 幂等：第二次什么都不改
  assert.equal(migrateLegacyLocalNodeId("home", storage), 0);
});

test("目标键已存在时优先保留新数据，旧键必须清掉", () => {
  // 1) 同一个 JSON 对象里 local:: 与 home:: 并存：新键的值获胜
  const storage = new MemoryStorage();
  storage.setItem(
    "mindfs-workspace-collapsed",
    JSON.stringify(["local::mindfs:/src", "home::mindfs:/src"]),
  );
  // 2) 存储键级冲突（带前缀的 vp-progress）
  storage.setItem("vp-progress:local::mindfs:src/a.ts", JSON.stringify({ q: "old" }));
  storage.setItem("vp-progress:home::mindfs:src/a.ts", JSON.stringify({ q: "new" }));

  migrateLegacyLocalNodeId("home", storage);

  assert.deepEqual(JSON.parse(storage.getItem("mindfs-workspace-collapsed")!), ["home::mindfs:/src"]);
  assert.deepEqual(JSON.parse(storage.getItem("vp-progress:home::mindfs:src/a.ts")!), { q: "new" });
  assert.equal(storage.getItem("vp-progress:local::mindfs:src/a.ts"), null, "旧键必须清掉，否则下次还会读到");
});

test("拿不到自报身份（仍是 local/空）时不动任何数据", () => {
  const storage = new MemoryStorage();
  storage.setItem("mindfs_active_node_id", LEGACY_SELF_NODE_ID);
  assert.equal(migrateLegacyLocalNodeId("", storage), 0);
  assert.equal(migrateLegacyLocalNodeId(LEGACY_SELF_NODE_ID, storage), 0);
  assert.equal(storage.getItem("mindfs_active_node_id"), LEGACY_SELF_NODE_ID);
});
