// 多账户分区：每个请求/WS 必须带上当前账户，且只带给自己这台服务器。
// 文本检查（仓库既有风格）+ 同源判定的行为复刻校验。
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const read = (p) => fs.readFileSync(path.join(root, p), "utf8");

const baseSrc = read("src/services/base.ts");
const authGateSrc = read("src/services/authGate.ts");

// 1) 账户参数必须由 base.ts 注入——它是全部 fetch / WS / 资源 URL 的唯一汇聚点。
//    漏了它，文件与图片的 src 会读到别的账户的数据。
assert.ok(baseSrc.includes("withAccountUser"), "base.ts 应定义 withAccountUser");
// 取某个导出函数的函数体（到下一个 export 前），避免用固定窗口猜长度
function bodyOf(name) {
  const start = baseSrc.indexOf(`export function ${name}(`);
  assert.ok(start >= 0, `base.ts 应导出 ${name}`);
  const next = baseSrc.indexOf("\nexport ", start + 1);
  return baseSrc.slice(start, next < 0 ? undefined : next);
}

for (const fn of ["appPath", "appURL", "wsURL"]) {
  assert.ok(
    bodyOf(fn).includes("withAccountUser"),
    `${fn} 必须走 withAccountUser——漏了 WS 会把所有连接落到主账户的 hub 上（实测踩过）`,
  );
}

// 2) 同源判定必须把 ws/wss 归一到 http/https。
//    直接用 origin 比较会让 `ws://host` 永不等于 `http://host`，
//    于是本机自己的 WS 被误判成跨节点、丢掉 user=，跨账户实时串流。
assert.ok(
  baseSrc.includes("isSameOriginAsPage"),
  "base.ts 应把同源判定收在 isSameOriginAsPage 里",
);
assert.ok(
  /protocol === "ws:"\s*\?\s*"http:"/.test(baseSrc) && /protocol === "wss:"\s*\?\s*"https:"/.test(baseSrc),
  "同源判定必须把 ws→http、wss→https 归一，否则 WS 会被当成跨节点",
);
assert.ok(
  !/target\.origin\s*!==\s*window\.location\.origin/.test(baseSrc),
  "不要再退回裸 origin 比较（ws 与 http 永不相等）",
);

// 3) 跨节点不带本机账户 id：账户表每台机器独立，带过去会让对方 404。
assert.ok(
  /if \(!isSameOriginAsPage\(url\)\) \{\s*return url;/.test(baseSrc),
  "非当前服务器发来的请求不得携带本机账户 id",
);

// 4) 账户态的唯一来源是 authGate，避免各处理散落的第二真源。
assert.ok(authGateSrc.includes("mindfs.current_user"), "账户态应存 mindfs.current_user");
assert.ok(
  authGateSrc.includes("export function currentUser"),
  "authGate 应导出 currentUser 供 base.ts 读取",
);

// 5) 同源判定行为复刻：与 base.ts 的 normalize 规则一致
function sameOrigin(targetUrl, pageUrl) {
  const t = new URL(targetUrl, pageUrl);
  const p = new URL(pageUrl);
  const n = (proto) => (proto === "ws:" ? "http:" : proto === "wss:" ? "https:" : proto);
  return n(t.protocol) === n(p.protocol) && t.host === p.host;
}

const page = "https://pc.example.com/mindfs/";
assert.ok(sameOrigin("wss://pc.example.com/mindfs/ws", page), "本机 wss 应判为同源");
assert.ok(sameOrigin("ws://127.0.0.1:7331/ws", "http://127.0.0.1:7331/mindfs/"), "本机 ws 应判为同源");
assert.ok(!sameOrigin("https://wsl.example.com/mindfs/ws", page), "别的节点应判为跨源");
assert.ok(!sameOrigin("wss://pc.example.com:8443/ws", page), "端口不同应判为跨源");
assert.ok(!sameOrigin("http://pc.example.com/mindfs/ws", page), "协议降级应判为跨源");

console.log("multi-account-partition: ok");
