import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 首屏窗口请求合流（CUSTOM(G-JU)）契约。
//
// 起因：打开会话有两个**顺序**加载者 —— App 的 restoreActiveSession 先拉尾部窗口，
// await 完才挂载 SessionViewer；后者 init effect 再拉一次同参数窗口。in-flight 去重
// 只能合并并发调用，所以第二次必然重新发。真机实测同一次打开里 `?latest=8` 在服务端
// 出现两次、跨节点各传 815 KB。
//
// 这一层是「结算后短窗复用」，必须守住三条边界：
//   ① 只复用成功结果 —— 失败要能立刻重试；
//   ② 有明确的时间窗，不能变成会话缓存（翻页/切回来必须拿最新）；
//   ③ 键必须含 nodeId 与全部窗口参数（同名项目跨节点、不同 beforeSeq 不能互相顶掉）。
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const fetchSrc = read("src/services/session/sessionWindowFetch.ts");

test("window fetch: settled result is reused only when the caller opts in", () => {
  // 复用必须由调用方显式请求（opts.reuseWithinMs），默认仍「每次重发」——
  // 否则「切回来/翻页」会拿到旧快照，那是 session-window-dedup 钉住的边界。
  assert.match(fetchSrc, /windowSettled/);
  assert.match(fetchSrc, /const reuseWithinMs = Number\(opts\?\.reuseWithinMs \|\| 0\);/);
  assert.match(fetchSrc, /if \(reuseWithinMs > 0\) \{/);
  assert.match(fetchSrc, /Date\.now\(\) - settled\.at < reuseWithinMs/);
  // 打开会话那条路必须真的开这个口子，否则这条定制等于没接。
  const lifecycle = read("src/app/useSessionLifecycle.ts");
  assert.match(lifecycle, /reuseWithinMs: OPEN_SESSION_WINDOW_REUSE_MS/);
  const service = read("src/services/session/sessionService.ts");
  assert.match(service, /export const OPEN_SESSION_WINDOW_REUSE_MS = \d+;/);
});

test("window fetch: only successful results are reused", () => {
  // 失败不进复用表：否则一次网络抖动会让接下来这段时间内都拿不到数据。
  assert.match(fetchSrc, /if \(value && reuseWithinMs > 0\)/);
  assert.match(fetchSrc, /windowSettled\.set\(inflightKey/);
});

test("window fetch: reuse key keeps nodeId and all window params", () => {
  assert.match(
    fetchSrc,
    /const inflightKey = `\$\{nodeId\}::\$\{rootId\}::\$\{sessionKey\}::\$\{opts\?\.beforeSeq \|\| 0\}::\$\{opts\?\.latest \|\| 0\}::\$\{limit\}`/,
  );
});
