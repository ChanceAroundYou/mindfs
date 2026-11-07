// agent9 / 上游痕迹守卫：身份、更新通道、版本语义必须各只有一个来源，且那个来源是我们自己。
//
// 与 upstream-removals.test.mjs 的分工：
//   · upstream-removals 钉「删掉的能力本体不许回来」（文件级复活、悬空引用）
//   · 本文件钉「身份与语义不许漂移」—— 同一件事在第二处写死、而那一处写着上游的值，
//     就是半吊子 repoint：改了一边、另一边的行为照旧，且没人知道有几边。
//
// 起点是实测事实（docs/plans/de-brand-and-ios-shell.md）：四个节点的自动更新检查曾挂在
// 上游 a9gent/mindfs 的发布通道上，界面显示「可更新到上游 v0.5.9（带 relay）」。
import assert from "node:assert";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

test("agent9-traces", async () => {
const root = path.resolve(fileURLToPath(import.meta.url), "../../..");
const read = (relOrAbs) =>
  fs.readFileSync(path.isAbsolute(relOrAbs) ? relOrAbs : path.join(root, relOrAbs), "utf8");
const EXPECTED_REPO = "ChanceAroundYou/mindfs";

// 1) 发布/更新身份：定义点只有一处（internal/deploy.ReleaseRepo），其余全由它派生。
assert.match(
  read("internal/deploy/release.go"),
  new RegExp(`const ReleaseRepo = "${EXPECTED_REPO}"`),
  `internal/deploy/release.go 的 ReleaseRepo 不再是 ${EXPECTED_REPO}（或常量被抄成了字面量）`,
);

const consumers = [
  ["Makefile", `RELEASE_REPO ?= ${EXPECTED_REPO}`, "Makefile 的发布/签名仓库身份"],
  ["scripts/install.sh", `REPO="${EXPECTED_REPO}"`, "install.sh 的下载源"],
  ["scripts/install.ps1", `$Repo = "${EXPECTED_REPO}"`, "install.ps1 的下载源"],
  ["web/src/services/update/githubAppUpdate.ts", `const RELEASE_REPO = "${EXPECTED_REPO}"`, "安卓壳读发布清单的仓库"],
  ["scripts/sign-release-manifest.go", `flag.String("repo", deploy.ReleaseRepo`, "签名清单的 -repo 缺省值"],
  ["scripts/android-version-manifest.go", `flag.String("repo", deploy.ReleaseRepo`, "安卓发布清单的 -repo 缺省值"],
];
for (const [file, needle, why] of consumers) {
  assert.ok(read(file).includes(needle), `${file}：${why}不再由 ReleaseRepo 派生（找 ${needle}）`);
}

// 2) 后端自动更新的两个装配点都必须走那个常量。
// 写回字面量等于把四个节点重新挂到别人的发布通道上 —— 而且只在运行时才看得出来。
function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    // build / assets：构建产物（生成的 bundle、gradle 输出），扫它们既慢又没有意义
    if (["node_modules", "dist", "build", "assets"].includes(entry.name) || entry.name.startsWith(".")) continue;
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(p, out);
    else if (/\.(go|ts|tsx|mjs|java|gradle|sh|ps1|json)$/.test(entry.name)) out.push(p);
  }
  return out;
}

const codeFiles = [
  ...walk(path.join(root, "server")),
  ...walk(path.join(root, "cli")),
  ...walk(path.join(root, "internal")),
  ...walk(path.join(root, "web/src")),
];
const newServiceSites = codeFiles.filter((p) => read(p).includes("update.NewService("));
assert.equal(newServiceSites.length, 2, `update.NewService 的调用点数变了：${newServiceSites.map((p) => path.relative(root, p)).join(", ")}`);
for (const p of newServiceSites) {
  assert.match(
    read(p),
    /update\.NewService\(\s*deploy\.ReleaseRepo\b/,
    `${path.relative(root, p)} 的更新通道又拿到了字面量仓库名`,
  );
}

// 3) 代码路径里不许再出现「别人的 mindfs 仓库」。
// 只扫代码与构建脚本：docs/ 与登记清单记录血缘（那是合上游时的判据，删了反而没人知道该留什么）。
// example/ 是刻意的合成夹具（如 server/internal/update/service_test.go 的 testReleaseRepo）——
// 测试数据故意不写真仓库名，否则夹具本身就成了第二处身份。
// LINEAGE_NOTES 里的两处是**注释里记录血缘**（「这里曾是 a9gent/mindfs、为什么必须只有一处定义」）：
// 它们描述上游而不是使用上游，删掉它们等于把「为什么改」的证据丢掉。
const REPO_LITERAL_OK = new Set(["ChanceAroundYou", "example"]);
const LINEAGE_NOTES = new Set(["internal/deploy/release.go", "scripts/check-upstream/main.go"]);
for (const p of [...codeFiles, path.join(root, "Makefile"), path.join(root, "web/package.json")]) {
  const rel = path.relative(root, p);
  if (LINEAGE_NOTES.has(rel)) continue;
  const src = read(p);
  // owner 的字符集刻意收窄：产物路径（"mindfs_v1.2.3/mindfs" 这种 dist 里的相对路径）不是仓库身份。
  for (const m of src.matchAll(/["'`]([A-Za-z][A-Za-z0-9-]*)\/mindfs["'`]/g)) {
    assert.ok(
      REPO_LITERAL_OK.has(m[1]),
      `${path.relative(root, p)} 又把 ${m[1]}/mindfs 当本构建的仓库（身份只有一个来源：internal/deploy.ReleaseRepo）`,
    );
  }
}

// 4) `a9gent` 字面量的允许位置（逐条点名，理由写在旁边）。
// 新增允许项要先想清楚：它是不是在**描述**上游，还是在**使用**上游。后者一律不许。
const A9GENT_ALLOW = new Map([
  ["docs/upstream-customizations.yaml", "定制清单：记录「这条曾来自上游、为什么必须留删」"],
  ["scripts/check-upstream/main.go", "血缘说明注释（工具靠 upstream ref 对账）"],
  ["internal/deploy/release.go", "注释记录旧缺陷：身份为什么必须只在这一处"],
  ["cli/cmd/autostart_unix.go", "卸载时清旧 macOS LaunchAgent 标签 com.a9gent.mindfs（存量安装的残留）"],
  ["scripts/install.sh", "卸载时清旧 LaunchAgent 的 plist 路径（同上）"],
  ["web/tests/upstream-removals.test.mjs", "黑名单声明本身"],
  ["web/tests/agent9-traces.test.mjs", "本文件的黑名单声明本身"],
  [".claude/skills/apk-release/SKILL.md", "坑位说明：GH_REPO 不显式指定会推到 upstream"],
  ["Makefile", "help 里标明 upstream ref 是 check-upstream 的依赖"],
]);
for (const rel of fs.readdirSync(path.join(root, "docs/plans"))) {
  if (rel.endsWith(".md")) A9GENT_ALLOW.set(`docs/plans/${rel}`, "历史计划：记录当时的实测事实");
}
A9GENT_ALLOW.set("docs/plans/README.md", "计划索引");

const scanDirs = ["server", "cli", "internal", "scripts", "web/src", "web/android/app/src/main/java", "web/tests", ".claude/skills"];
const scanned = new Set();
for (const rel of scanDirs) {
  walk(path.join(root, rel)).forEach((p) => scanned.add(p));
}
for (const rel of ["README.md", "README.zh.md", "Makefile", "agents.json", "release-notes.md", "AGENTS.md"]) {
  scanned.add(path.join(root, rel));
}
for (const p of scanned) {
  const rel = path.relative(root, p);
  if (!fs.existsSync(p) || !/\.(go|ts|tsx|mjs|java|gradle|sh|ps1|json|md|yaml)$/.test(p)) continue;
  const src = read(rel);
  if (!/a9gent/i.test(src)) continue;
  assert.ok(
    A9GENT_ALLOW.has(rel),
    `${rel} 又出现了 agent9 / 上游身份痕迹：${(src.match(/[^\n]*a9gent[^\n]*/gi) || []).slice(0, 2).join(" | ")}`,
  );
}

// 5) 已删能力的配置键不许留在配置与类型里（「删了一半」的形态：键在、没人读）。
assert.ok(!read("agents.json").includes("tokenStationURL"), "agents.json 又写回了 relay 时代的 tokenStationURL");
for (const p of walk(path.join(root, "server"))) {
  // 钉接线而不是文字：config_types.go 的注释要解释「为什么没有这个字段」，那是判据。
  assert.ok(
    !/TokenStationURL\s+string|json:"tokenStationURL"/.test(read(p)),
    `${path.relative(root, p)} 又解析已删的 tokenStationURL 配置键`,
  );
}
for (const rel of ["scripts/install.sh", "scripts/install.ps1"]) {
  const src = read(rel);
  assert.ok(!/RELAY_DOWNLOAD_BASE|FALLBACK_URL|FallbackUrl/.test(src), `${rel} 又留了对上游服务器的兜底外连`);
}

// 6) 版本语义只有一条公式：Go 比较器、gradle 的 versionCode、前端的更新提示必须同步。
// 三处各自实现过一遍，就会出现「后端说有新版本、安卓安装器说更旧、登录页说已是最新」。
const gradle = read("web/android/app/build.gradle");
const tsVersion = read("web/src/services/update/appVersion.ts");
const goVersion = read("server/internal/update/version.go");
const FORMULA = "major * 100000000 + minor * 1000000 + patch * 10000 + build + 1";
assert.ok(gradle.includes(FORMULA), "gradle 的 versionCode 公式变了，前端 appVersion.ts 得跟着改");
assert.ok(tsVersion.includes(FORMULA), "前端 versionCode 公式与 gradle 不再一致");
// 构建号是 tag 尾号，所以 `-` 必须是分隔符；git describe 的装饰（-<n>-g<sha>）必须整块剥掉。
assert.ok(/r == '\.' \|\| r == '-'/.test(goVersion), "Go 版本比较又不把 - 当分隔符（tag 尾号会看不见）");
assert.ok(/describeDecoration/.test(goVersion) && /describeDecoration/.test(tsVersion), "git describe 装饰的剥离只在一侧（另一侧会把提交数读成版本段）");

// 前端只有一处实现：githubAppUpdate / appUpdate 都必须从 appVersion.ts 取，不得各自抄一份。
const githubUpdate = read("web/src/services/update/githubAppUpdate.ts");
const shellUpdate = read("web/src/services/update/appUpdate.ts");
assert.match(githubUpdate, /import \{[^}]*nativeBuildCodeFromTag[^}]*\} from "\.\/appVersion"/, "githubAppUpdate 又自己算 versionCode");
assert.match(shellUpdate, /import \{[^}]*isNewerVersion[^}]*\} from "\.\/appVersion"/, "appUpdate 又自己实现版本比较");
for (const [rel, src] of [["githubAppUpdate.ts", githubUpdate], ["appUpdate.ts", shellUpdate]]) {
  assert.ok(!/function isNewerVersion|const isNewerVersion =/.test(src), `${rel} 又抄了一份版本比较实现`);
  assert.ok(!/minor \* 1000000 \+/.test(src), `${rel} 又抄了一份 versionCode 公式`);
}

console.log("✓ 身份单源、更新通道走常量、无 agent9 痕迹、版本公式一处定义");
});
