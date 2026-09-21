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
const apiSrc = read("src/services/api.ts");
const appSrc = read("src/App.tsx");

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
  baseSrc.includes("isSameServerAsPage"),
  "base.ts 应把同源判定收在 isSameServerAsPage 里",
);
assert.ok(
  /protocol === "ws:"\s*\?\s*"http:"/.test(baseSrc) && /protocol === "wss:"\s*\?\s*"https:"/.test(baseSrc),
  "同源判定必须把 ws→http、wss→https 归一，否则 WS 会被当成跨节点",
);
assert.ok(
  !/target\.origin\s*!==\s*window\.location\.origin/.test(baseSrc),
  "不要再退回裸 origin 比较（ws 与 http 永不相等）",
);

// 2b) 绝不能把「本机节点表里的地址」当成同源。
//     实测 home 与 pc 是**两台不同的机器**，各自的节点表里恰好都有指向对方的条目；
//     按表放宽会把本机账户 id 发给没有该账户的机器 → 对方 404 → 该项目全空。
//     （这条曾真的写错过，靠 API 实测才发现，见 memory/cross-machine-account-identity）
assert.ok(
  !/getNodes\(\)\.some/.test(baseSrc) && !/isCurrentServerOrigin/.test(baseSrc),
  "base.ts 不得用本机节点表放宽同源判定——表里的远端条目正是别的机器",
);

// 2c) 跨机器的 unknown_user 不得触发登出；本机账户被删时必须登出。
//     `unknown_user` 只该由**本机**产生（对方机器没有本账户时回的是空列表，见 2e），
//     所以这里只剩「本机账户被删」一种含义：必须登出，否则整页卡在 404。
assert.ok(
  /if \(!targetsPageServer\(input\)\) \{\s*return;/.test(apiSrc),
  "非本机服务器返回的 unknown_user 不得清登录态（否则换个节点就被踢下线）",
);
assert.ok(
  apiSrc.includes("targetsPageServer"),
  "api.ts 应把「这条请求打的是不是本机」判在 targetsPageServer 里",
);

// 2e) 「对方机器没有这个账户」是**空列表**，不是 404。
//     账户只是可见性设置，按账户的数据本来就是物理隔离的（各账户各自的
//     registry.json / meta 目录），所以未知账户等价于「它的目录里没有数据」。
//     服务端因此照常构建工作区，读者自然拿到空——前端不需要任何特殊分支。
assert.ok(
  !apiSrc.includes("isUnknownUserError"),
  "不应再有 isUnknownUserError：跨机器缺账户已改为空列表，前端无需按节点区分",
);
assert.ok(
  !appSrc.includes("account_missing"),
  "App.tsx 不应再有「缺账户」提示/隐藏节点/一键建号那套（账户不是身份）",
);
assert.ok(
  !/nodeHasAccount|createAccountOnNode/.test(appSrc),
  "App.tsx 不应再做「对方有没有这个账户」的客户端预判——判定收归服务端数据本身",
);

// 2d) 三个 JSON helper 必须**都**走 handleAccountGone。
//     protectedJSON 曾漏掉：本机账户被删时它不登出，整页卡在 404 出不来，
//     而 /api/dirs 恰好走的就是它。
for (const fn of ["fetchJSON", "fetchMaybeJSON", "protectedJSON"]) {
  const start = apiSrc.indexOf(`export async function ${fn}<`);
  assert.ok(start >= 0, `api.ts 应导出 ${fn}`);
  const next = apiSrc.indexOf("\nexport ", start + 1);
  const body = apiSrc.slice(start, next < 0 ? undefined : next);
  assert.ok(
    body.includes("handleAccountGone"),
    `${fn} 必须走 handleAccountGone——漏了会让本机账户被删后整页卡在 404`,
  );
}

// 3) 跨机器：**带用户名、不带 id**。
//    不带的话对方服务端回落到它**自己的主账户**，把别人机器上的项目当成你的返回回来，
//    不报错不提示（实测踩过，比 404 危险）。
//    带 id 也不行：账户表每台机器独立、id 随机生成，对方必然 404。
assert.ok(
  /if \(isSameServerAsPage\(url\)\) \{\s*return appendQuery\(url, `user=\$\{encodeURIComponent\(user\.id\)\}`\);/m.test(baseSrc),
  "同源请求应带账户 id",
);
assert.ok(
  baseSrc.includes("const name = String(user.username || \"\").trim();"),
  "跨机器应改带用户名——服务端按用户名解析到它本地的同名账户",
);
assert.ok(
  /user=\$\{encodeURIComponent\(name\)\}/.test(baseSrc),
  "跨机器要真的把用户名写进 user=",
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

// 6) 相对路径必须解析成「同机」。handleAccountGone 靠在它上面判断该不该登出：
//    本机账户被删时请求常是相对路径，若误判成跨机就永远不登出、整页卡在 404。
assert.ok(sameOrigin("/mindfs/api/dirs?user=u_x", page), "相对路径应解析为同机");
assert.ok(sameOrigin("", page), "空串（当前页）应解析为同机");
// 绝对跨机地址在任意页面下都判跨机
assert.ok(!sameOrigin("https://other.example.com/mindfs/api/dirs", page), "绝对跨机地址应判跨机");

console.log("multi-account-partition: ok");
