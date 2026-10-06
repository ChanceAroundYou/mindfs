import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * git 客户端缓存上限的契约守卫（`Scope: G-AH`）。
 *
 * 这一层守的是「标签页闪退」里一条真实的内存/配额上涨路径：
 * `gitCommitDiffCache` 存的是**完整 diff 正文**，长期开着标签页翻 git 历史会一路涨；
 * localStorage 侧则写满配额后被 `try/catch` 静默吞掉，表现为「缓存莫名丢了」。
 *
 * 上限本身是**行为**，但它藏在没有导出的内部函数里，所以这里钉的是形状：
 * 一旦有人把它「简化」掉，性能/稳定性是静默退化的（测试全绿、短会话看不出问题）。
 */

const root = path.resolve(import.meta.dirname, "..");
const source = fs.readFileSync(path.join(root, "src/services/git.ts"), "utf8");

// 1) 有界 set 必须存在且有明确上限。
assert.match(
  source,
  /const gitCacheMaxEntries = 200;/,
  "the in-memory git caches need an explicit bound",
);
assert.match(
  source,
  /function setBounded<K, V>\(cache: Map<K, V>, key: K, value: V\): void/,
  "the bounded setter must exist",
);

// 2) 三个缓存都必须走 setBounded —— 残留任何一处裸 .set 就会让上限形同虚设。
for (const name of ["gitHistoryListCache", "gitCommitFilesCache", "gitCommitDiffCache"]) {
  assert.doesNotMatch(
    source,
    new RegExp(`${name}\\.set\\(`),
    `${name} must be written through setBounded, never directly`,
  );
}
assert.equal(
  (source.match(/setBounded\(gitHistoryListCache, /g) || []).length,
  2,
  "gitHistoryListCache has two write sites (bare -> scoped migration + normal)",
);
assert.equal(
  (source.match(/setBounded\(gitCommitFilesCache, /g) || []).length,
  2,
  "gitCommitFilesCache has two write sites (persisted load + fresh fetch)",
);
assert.equal(
  (source.match(/setBounded\(gitCommitDiffCache, /g) || []).length,
  2,
  "gitCommitDiffCache has two write sites (persisted load + fresh fetch)",
);

// 3) localStorage 侧同样要有上限，且两处写入点都要调用它。
assert.match(
  source,
  /const commitDiffStorageMaxEntries = 32;/,
  "localStorage persistence needs an explicit bound",
);
assert.match(
  source,
  /function capStorageByPrefix\(prefix: string, maxEntries: number\): void/,
  "the storage cap helper must exist",
);
assert.equal(
  (source.match(/capStorageByPrefix\(COMMIT_FILES_STORAGE_PREFIX,/g) || []).length,
  1,
  "commit files storage must be capped at the write site",
);
assert.equal(
  (source.match(/capStorageByPrefix\(COMMIT_DIFF_STORAGE_PREFIX,/g) || []).length,
  1,
  "commit diff storage must be capped at the write site",
);

// 4) in-flight map **刻意不设上限** —— 它们在 .finally 里就删了，是瞬时的。
//    这条断言是防止有人「顺手」给它们加上限（没有意义，还会让并发去重失灵）。
assert.match(source, /gitCommitDiffInflight\.delete\(/, "in-flight entries must be cleared in finally");
assert.match(source, /gitCommitFilesInflight\.delete\(/, "in-flight entries must be cleared in finally");

console.log("git-cache-bounds.test.mjs: OK");
