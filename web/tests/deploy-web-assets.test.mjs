// 部署脚本的 web 资产不变量（G-AL）。这些是 shell 里的约定 —— 没有编译器、没有类型系统
// 看着它们，而它们跑在远端，坏了要等用户在开着页面的浏览器里撞上才算发现。
//
// 每条对应一个真实发生过的坏法：
//   1 `rm -rf` web 目录      → 部署前就打开的标签页懒加载旧 chunk 时 404（73db73e 修的就是这个）
//   2 按龄清理的 TTL 两处不一致 → 改了一处忘了另一处，要么删太快要么永远不删
//   3 WSL 端重新编译/拉源码  → 两边版本号漂移（2026-10-06 起 WSL 只收产物）
//   4 WSL 端收 web/dist      → 纯 worker 不服务前端，多推的几 MB 是纯负担（2026-10-07 起不再推）
//   5 add 白名单漏根级文档   → 改了 CLAUDE.md 跑 ship 后没被提交，工作区留脏、版本号挂 -dirty
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(fileURLToPath(import.meta.url), "../../..");
const read = (p) => fs.readFileSync(path.join(root, p), "utf8");

const deploy = read("scripts/deploy-all.sh");
const installSh = read("scripts/install.sh");
const makefile = read("Makefile");

// 1) 装 web 资源时不得整目录删除。
//    注意 uninstall 路径下的 `rm -rf "$share_dir"` 是合法的 —— 它删的是 share/mindfs，不是 web/，
//    所以这里只匹配以 web 结尾的目标。
for (const [name, src] of [["scripts/deploy-all.sh", deploy], ["scripts/install.sh", installSh]]) {
  for (const line of src.split("\n")) {
    const t = line.trim();
    if (t.startsWith("#") || !t.includes("rm -rf")) continue;
    assert.ok(
      !/rm\s+-rf\s+\S*\/web\/?\s*("|'|$)/.test(t),
      `${name} 用 rm -rf 删掉了 web 目录（哈希资源 immutable，会让已打开的页面懒加载 404）：${t}`,
    );
  }
}

// 覆盖复制 + 按龄清理的组合必须都在（只剩本机这一份 —— WSL 是纯 worker，不再装 web）。
assert.doesNotMatch(deploy, /cp -R ~\/\$STAGE\/dist/, "deploy-all.sh 不该再往 WSL 复制 dist（worker 不服务前端）");
assert.doesNotMatch(deploy, /-C "\$ROOT\/web" dist/, "deploy-all.sh 的 tar 不该再带上 web/dist（worker 不服务前端）");
assert.ok(/cp -R "\$\(WEB_DIR\)\/dist\/\." /.test(makefile), "Makefile install 应覆盖复制 dist（结尾 /. 防 web/dist 嵌套）");
assert.ok(/cp -r "\$\{PKG_DIR\}\/web\/\." /.test(installSh), "install.sh 应覆盖复制 web");

// 2) TTL 两处必须一致。Makefile 是带名字的变量，install.sh 是字面量 —— 字面量正是会漂的那个。
//    deploy-all.sh 不再参与：它不再往 WSL 装 web 资源（2026-10-07 起 WSL 是纯 worker）。
const ttl = makefile.match(/WEB_ASSET_TTL_DAYS \?= (\d+)/)?.[1];
assert.ok(ttl, "Makefile 应定义 WEB_ASSET_TTL_DAYS");
assert.deepStrictEqual(
  [ttl, installSh.match(/-mtime \+(\d+) -delete/)?.[1]],
  [ttl, ttl],
  `按龄清理的 TTL 不一致（Makefile=${ttl}）`,
);

// 3) WSL 只收产物，不在那边编译。
//    deploy-all.sh 里唯一允许的 make build 是本机那次（第 90 行附近，不经 ssh）；
//    远端脚本段内不得出现 make / git pull / npm。
const remoteBlock = deploy.slice(deploy.indexOf("REMOTE_SCRIPT="), deploy.indexOf("\nREMOTE\n"));
assert.ok(remoteBlock.length > 0, "找不到 deploy-all.sh 的远端脚本段");
for (const banned of [/\bmake\b/, /git\s+pull/, /\bnpm\b/]) {
  assert.ok(!banned.test(remoteBlock), `远端脚本段不得编译/拉源码（WSL 侧无源码库）：${banned}`);
}
assert.ok(remoteBlock.includes("~/.local/bin/mindfs --version"), "远端脚本应回读版本号供对账");

// 版本号对账：推的必须是刚构建的同一个二进制，两边版本逐字相同
assert.ok(/\[\[ "\$REMOTE_VERSION" == "\$VERSION" \]\]/.test(deploy), "deploy-all.sh 应逐字对账两端版本号");

// 4) 提交白名单必须覆盖根级文档。`docs/` 只够得到 docs/ 目录，够不到仓库根 ——
//    漏了 CLAUDE.md 时改动静默留在工作区，版本号挂上 `-dirty`（2026-10-07 实测）。
//    config.json 刻意不在名单里：它会被本机改 role/端口，提交等于推本地运行配置。
const addCmd = deploy.match(/git add -A --[\s\S]*?2>\/dev\/null/)?.[0] ?? "";
assert.ok(addCmd, "找不到 deploy-all.sh 的 git add 白名单");
for (const f of ["CLAUDE.md", "README.md", "README.zh.md", "release-notes.md"]) {
  assert.ok(addCmd.includes(f), `git add 白名单必须含根级文档 ${f}（docs/ 够不到仓库根）`);
}
assert.ok(!addCmd.includes("config.json"), "config.json 不该进白名单（会被本机改 role，提交等于推本地配置）");

console.log("✓ 部署脚本不变量成立：无 rm -rf web、TTL 两处一致(" + ttl + "d)、WSL 只收产物且不收 dist、add 白名单含根级文档");
