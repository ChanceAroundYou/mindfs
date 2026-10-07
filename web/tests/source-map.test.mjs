// source-map 的针对性测试：钉住「逻辑模块 → 物理文件」映射的完整性与重定向机制本身。
//
// 这张表是 43 个源码守卫测试的公共地基，它一旦失效（patch 没挂上 / 映射指向不存在的
// 文件 / 拆分后漏登记），表现是「断言静默扫到旧文件或空文件」而不是报错，所以必须自证。
import assert from "node:assert/strict";
import fs from "node:fs";
import { readFileSync } from "node:fs"; // 具名导入：patch 必须同时覆盖它（实测易漏）
import path from "node:path";
import { MODULES, WEB_ROOT, readSource, toLogical } from "./source-map.mjs";

// 1. 映射完整性：每个登记的逻辑模块至少一个物理文件，且物理文件真实存在。
for (const [logical, files] of Object.entries(MODULES)) {
  assert.ok(files.length > 0, `${logical} 至少要有一个物理文件`);
  for (const file of files) {
    assert.ok(
      fs.existsSync(path.join(WEB_ROOT, file)),
      `${logical} 指向的物理文件不存在：${file}`,
    );
  }
}

// 2. patch 生效：namespace 导入与具名导入都要被重定向。
//    临时把 src/App.tsx 指到 src/main.tsx，读到的应是 main.tsx 的内容。
const PROBE_KEY = "src/App.tsx";
const backup = MODULES[PROBE_KEY];
MODULES[PROBE_KEY] = ["src/main.tsx"];
try {
  const expected = fs.readFileSync(path.join(WEB_ROOT, "src/main.tsx"), "utf8");
  const viaNamespace = fs.readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
  const viaNamed = readFileSync(path.join(WEB_ROOT, "src/App.tsx"), "utf8");
  assert.equal(viaNamespace, expected, "namespace fs.readFileSync 必须被重定向");
  assert.equal(viaNamed, expected, "具名 readFileSync 必须被重定向");
} finally {
  if (backup === undefined) {
    delete MODULES[PROBE_KEY];
  } else {
    MODULES[PROBE_KEY] = backup;
  }
}

// 3. 未登记的路径必须透传原始文件，不能被误重定向。
const passthrough = fs.readFileSync(path.join(WEB_ROOT, "package.json"), "utf8");
assert.match(passthrough, /"name":\s*"mindfs-web"/, "未登记路径应读到真实文件");

// 4. readSource 对多文件模块做拼接；对未登记路径等价于单文件读取。
MODULES["__probe__"] = ["src/main.tsx", "package.json"];
try {
  const joined = readSource("__probe__");
  assert.match(joined, /createRoot/, "拼接应含第一个文件内容");
  assert.match(joined, /"name":\s*"mindfs-web"/, "拼接应含第二个文件内容");
} finally {
  delete MODULES["__probe__"];
}
assert.equal(readSource("package.json"), passthrough, "未登记路径 readSource 等价单文件");

// 5. toLogical：web/ 内的绝对路径归一成 POSIX 相对路径；外部路径与 fd 返回 null。
assert.equal(toLogical(path.join(WEB_ROOT, "src", "App.tsx")), "src/App.tsx");
assert.equal(toLogical(new URL("../src/App.tsx", import.meta.url)), "src/App.tsx");
assert.equal(toLogical("/etc/hosts"), null);
assert.equal(toLogical(42), null);
