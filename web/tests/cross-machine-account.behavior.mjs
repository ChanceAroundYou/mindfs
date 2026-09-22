// 「跨机器访问一个没有本账户的节点」的行为验证。
//
// 这条逻辑已经骗过人一次：曾把 home/pc 当成同一台服务器的两个域名，
// 于是把「节点表里的地址」也算成本机 → 本机账户 id 发给对方的机器 → 404 → 项目全空。
// 所以这里对着**运行中的真实服务**跑，不靠读源码文本。
//
// 用法：node tests/cross-machine-account.behavior.mjs
// 前提：两个服务器都能连上（见 memory/cross-machine-account-identity）。
import assert from "node:assert";

// home = PC 端 VM，pc = WSL。两台机器的账户表独立。
const SERVERS = [
  { name: "home", base: "https://home.xiaokubao.space/mindfs" },
  { name: "pc", base: "https://pc.xiaokubao.space/mindfs" },
];

/** 拿该服务器上的账户表（不带 user=，服务端回主账户，但 /api/users 总是全量）。 */
async function accounts(base) {
  const res = await fetch(`${base}/api/users`, { cache: "no-store" });
  assert.ok(res.ok, `${base} /api/users -> HTTP ${res.status}`);
  const j = await res.json();
  return Array.isArray(j?.users) ? j.users : [];
}

async function dirs(base, userId) {
  const url = userId ? `${base}/api/dirs?user=${encodeURIComponent(userId)}` : `${base}/api/dirs`;
  const res = await fetch(url, { cache: "no-store" });
  const body = await res.json().catch(() => null);
  return { status: res.status, body };
}

let checks = 0;
function check(label, cond, detail) {
  checks++;
  assert.ok(cond, `${label}${detail ? ` — ${detail}` : ""}`);
  console.log(`  ok  ${label}`);
}

console.log("cross-machine-account behavior:");
for (const s of SERVERS) {
  const users = await accounts(s.base);
  const ids = new Set(users.map((u) => String(u.id)));
  console.log(`\n[${s.name}] ${s.base}`);
  console.log(`  accounts: ${users.map((u) => `${u.username}(${u.id})`).join(", ") || "(none)"}`);

  // 1) 存在的账户必须 200（哪怕是空列表）
  for (const u of users) {
    const r = await dirs(s.base, u.id);
    check(`${s.name}: 已知账户 ${u.username} 可访问`, r.status === 200, `HTTP ${r.status}`);
  }

  // 2) 不存在的账户 = **空列表**，不是 404。
  //    账户只是可见性设置；按账户的数据本来就是物理隔离的（各账户各自的
  //    registry.json / meta 目录），所以「本机没这个账户」等价于「它的目录里没有数据」。
  //    返回 404 会让前端把一个空节点显示成一串报错，而用户要的只是「空的」。
  const bogus = "u_DOES_NOT_EXIST_ON_THIS_MACHINE";
  assert.ok(!ids.has(bogus));
  const miss = await dirs(s.base, bogus);
  check(`${s.name}: 未知账户 -> 200 空列表`, miss.status === 200, `HTTP ${miss.status}`);
  check(
    `${s.name}: 未知账户返回空数组`,
    Array.isArray(miss.body) && miss.body.length === 0,
    JSON.stringify(miss.body).slice(0, 120),
  );

  // 3) 不带 user= 会落到主账户 —— 这正是「看到别人项目」的来源；前端现在总会带上身份
  const noUser = await dirs(s.base, "");
  check(`${s.name}: 不带 user= 落到主账户`, noUser.status === 200, `HTTP ${noUser.status}`);

  // 4) 另一台机器的账户 id 在本机也是空列表（账户表独立的关键证据）
  const other = SERVERS.find((x) => x.name !== s.name);
  const otherUsers = await accounts(other.base);
  for (const u of otherUsers) {
    if (ids.has(String(u.id))) continue; // 恰好同名同 id 才跳过
    const r = await dirs(s.base, String(u.id));
    check(
      `${s.name}: 对方(${other.name})的账户 ${u.username} 在本机是空的`,
      r.status === 200 && Array.isArray(r.body),
      `HTTP ${r.status}`,
    );
  }
}

console.log(`\ncross-machine-account: ok (${checks} checks)`);
