import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

// e2e/live/ 把**线上凭证**放在仓库根的 `.env` 里（见 e2e/live/README.md）。
// 它必须被 .gitignore 挡住 —— 这个文件一旦入库，口令就随仓库走到每台机器和每个
// worktree，而且 `git add -A`（deploy-all.sh 的白名单兜底分支）会顺手带上它。
//
// 钉住的是一条**看不见的**约定：没有任何代码会因为 `.env*` 丢了而报错，
// 只有下一次 `git status` 里多出一行 `?? .env` —— 而那时可能已经被提交了。
test("live-env-ignored", async () => {
  const root = path.resolve(fileURLToPath(import.meta.url), "../../..");
  const gitignore = fs.readFileSync(path.join(root, ".gitignore"), "utf8");

  const lines = gitignore.split("\n").map((line) => line.trim());
  assert.ok(
    lines.includes(".env*"),
    ".gitignore 必须挡住 .env*：e2e/live/ 的线上凭证就放在仓库根的 .env 里",
  );
  // 放行 web/.env 是为了 Makefile 的 -include（只有部署前缀，无密钥）；
  // 这条放行如果被改成 `.env` 或范围更大的写法，根 .env 就会重新暴露。
  assert.ok(
    lines.includes("!web/.env"),
    "只允许 web/.env 例外（Makefile 读部署前缀），例外范围不能放大",
  );

  const live = path.join(root, "e2e", "live");
  assert.ok(fs.existsSync(path.join(live, "harness.py")), "e2e/live/harness.py 必须存在");
  assert.ok(fs.existsSync(path.join(live, "README.md")), "e2e/live/README.md 必须说明跑法");
  // 凭证只从 .env / 环境变量读，不许写进脚本。
  const harness = fs.readFileSync(path.join(live, "harness.py"), "utf8");
  assert.match(harness, /MINDFS_LIVE_PASS/, "凭证键名固定为 MINDFS_LIVE_PASS");
  assert.doesNotMatch(
    harness,
    /MINDFS_LIVE_PASS\s*=\s*["'][^"']+["']/,
    "口令不能硬编码进 harness.py",
  );
});
