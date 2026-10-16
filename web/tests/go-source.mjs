// Go 源码守卫的「按包读」助手。
//
// 为什么不是「读某个 .go 文件」：源码守卫钉的是「这段实现还在不在」，不是「它住在
// 哪个文件名里」。2026-10-16 的 B1–B10 拆分把大文件切成职责文件之后，钉文件名的
// 守卫集体假红 —— 实现一行没少，只是搬了家（`http.go` → `http_routes.go`、
// `stream_hub.go` → `stream_hub_build.go`、`appcontext.go` → `appcontext_broadcast.go`
// …）。所以 Go 侧的守卫一律按**包**读。
//
// 这与 `session-status-badges.test.mjs` 里的写法一致，只是抽出来共用：
// 7 个守卫各写一遍 readdir 没有意义，而且下次拆分还会漏。
import fs from "node:fs";
import path from "node:path";

/** 仓库根目录（web/tests/ 往上两层）。 */
export const REPO_ROOT = path.resolve(import.meta.dirname, "../..");

/**
 * 读一个 Go 包的全部非测试源码，按文件名排序后拼成一份文本。
 *
 * 排序是为了让拼接顺序稳定：守卫里有按 `indexOf` 比先后的断言（例如「先合后拆」），
 * 顺序不定的话同一份代码会时而绿时而红。
 */
export function readGoPackage(relDir) {
  const dir = path.join(REPO_ROOT, relDir);
  return fs
    .readdirSync(dir)
    .filter((name) => name.endsWith(".go") && !name.endsWith("_test.go"))
    .sort()
    .map((name) => fs.readFileSync(path.join(dir, name), "utf8"))
    .join("\n");
}
