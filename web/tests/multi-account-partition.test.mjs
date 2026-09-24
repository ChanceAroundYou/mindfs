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
// 2026-09 App.tsx 拆分：顶层存储键工具移到 app/appSupport.tsx（账户分区合同随文件走）。
// 2026-09 二次拆分：appSupport.tsx 拆成 appStorage / appPath / appSession / appTask / appMisc，
// 账户分区合同随 appStorage.ts 走；负向断言改为扫全部 app 模块（比只扫单文件更强）。
// 注：过渡门面 appSupport.tsx 已在同批删除，下列为全部实际模块。
const supportSrc = read("src/app/appStorage.ts");
const appSupportModules = [
  "src/app/appStorage.ts",
  "src/app/appPath.ts",
  "src/app/appSession.ts",
  "src/app/appTask.ts",
  "src/app/appMisc.tsx",
].map(read);

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

// 3b) **缓存必须按账户分区**。
//     服务端分区正确不代表看不到别人的东西：多项目会话列表是「先渲染 IndexedDB 缓存、
//     再补发账户作用域请求」，若缓存键不含账户，切到新账户时会先把上一账户的全量会话画出来，
//     而后到的空结果只是 merge 进 prev、并不清空它（实测：项目列表已空、会话列表仍是全部）。
//     同一个坑对单项目列表缓存同样成立。
const sessionSrc = read("src/services/session.ts");
assert.ok(
  /function multiRootSessionListCacheKey\(\)[\s\S]{0,400}currentUser\(\)\?\.username/.test(sessionSrc),
  "多项目会话缓存键必须带账户——常量键会让新账户看到旧账户的全部会话",
);
assert.ok(
  !/MULTI_ROOT_SESSION_LIST_CACHE_KEY/.test(sessionSrc),
  "不应再有账户无关的 MULTI_ROOT_SESSION_LIST_CACHE_KEY 常量",
);
assert.ok(
  /function buildSessionListCacheKey[\s\S]{0,400}sessionListCacheScope\(\)/.test(sessionSrc),
  "单项目列表缓存键也必须带账户",
);

// 3c) 「拉取失败」与「成功但为空」不能混为一谈。
//     两者都得到 []，若把空结果当失败，合并逻辑会保留该节点**上一次（另一个账户的）**分组。
//     真正该保留旧分组的只有请求抛错的情况。
assert.ok(
  /failed\[i\] = true;/.test(appSrc) && /failed\[i\] = false;/.test(appSrc),
  "多项目会话加载必须分别记录成功为空与请求失败",
);
assert.ok(
  !/nodeFetchResults\[i\] \|\| \[\]\)\.length === 0/.test(appSrc),
  "不得再用「返回长度为 0」判定节点失败——空是合法结果",
);

// 3d) 「上次打开的项目」也必须按账户存。
//     它不分区的话，换账户后会恢复**上一个账户**的项目为当前项目，
//     随后拿它去请求会话/看板 → 新账户根本没这个项目 → 登录后直接报错（实测踩过）。
assert.ok(
  /function accountScopedKey\(base: string\)[\s\S]{0,300}currentUser\(\)\?\.username/.test(supportSrc),
  "last-root 存储键必须带账户",
);
// 扫全部 app 模块：门面拆开后，裸 key 的误用可能落在任何一个新文件里，
// 只查单个文件等于把守卫削弱成「那个文件恰好干净」。
assert.ok(
  appSupportModules.every(
    (src) =>
      !/localStorage\.setItem\(LAST_ROOT_STORAGE_KEY/.test(src) &&
      !/localStorage\.getItem\(LAST_ROOT_STORAGE_KEY\)/.test(src),
  ),
  "不得再直接读写裸的 LAST_ROOT_STORAGE_KEY（必须走 accountScopedKey）",
);

// 3e) 账户管理必须打**页面服务器**，不能跟随当前选中的节点。
//     账户表每台机器一份，而登录走的是页面服务器（authGate.authBaseURL）。
//     跟随节点的话，「登录到 home、当前节点是 pc」会让面板列出 pc 的账户，
//     在 home 建的账户看起来就"消失"了，而身份其实还在 home。
const accountsSrc = read("src/services/accounts.ts");
assert.ok(
  accountsSrc.includes("pageServerPath"),
  "账户管理应走 pageServerPath（页面服务器），而不是跟随当前节点的 appPath",
);
assert.ok(
  !/appPath\(/.test(accountsSrc),
  "accounts.ts 不应再用 appPath——它跟随当前节点，会列错机器的账户",
);
assert.ok(
  /export function pageServerPath\(path: string\)[\s\S]{0,600}withAccountUser/.test(baseSrc),
  "pageServerPath 也必须走 withAccountUser（账户参数唯一汇聚点）",
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
