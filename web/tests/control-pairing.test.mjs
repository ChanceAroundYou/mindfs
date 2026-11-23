import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  SYNC_TOKEN_MIN_BYTES,
  isSyncTokenShape,
  normalizePairingDraft,
  pairingDraftIssue,
  toPairingRequest,
} from "../src/shared/controlPairing.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const service = read("../src/services/net/controlSync.ts");
const shared = read("../src/shared/controlPairing.ts");
const form = read("../src/components/app/ControlPairingGate.tsx");
const banner = read("../src/components/app/ControlModeBanner.tsx");
const hook = read("../src/hooks/useControlSyncStatus.ts");
const zh = read("../src/i18n/locales/zh-CN-workspace.ts");
const en = read("../src/i18n/locales/en-US-workspace.ts");

const draft = (primaryURL, token, caFile = "") => ({ primaryURL, token, caFile });

test("配对输入按服务端同一套规则本地校验", () => {
  // 地址：空 → url，解析不出 → url，明文 http → urlScheme。
  assert.equal(pairingDraftIssue(draft("", "0123456789abcdef")), "url");
  assert.equal(pairingDraftIssue(draft("   ", "0123456789abcdef")), "url");
  assert.equal(pairingDraftIssue(draft("home.example:7331", "0123456789abcdef")), "url");
  assert.equal(pairingDraftIssue(draft("http://home.example:7331", "0123456789abcdef")), "urlScheme");
  // 同步通道搬的是整份控制面（含口令 hash）：明文 http 服务端也硬性拒绝。
  assert.equal(pairingDraftIssue(draft("ftp://home.example", "0123456789abcdef")), "urlScheme");
  // 合法输入必须放行 —— 否则用户填对了也被前端挡住。
  assert.equal(pairingDraftIssue(draft("https://home.example:7331", "0123456789abcdef")), null);
  // 地址先于 token：表单按从上到下报第一个错。
  assert.equal(pairingDraftIssue(draft("", "")), "url");
});

test("token 形状与 ValidateSyncToken 同口径：≥16 字节、无空白", () => {
  assert.equal(SYNC_TOKEN_MIN_BYTES, 16);
  assert.equal(isSyncTokenShape("0123456789abcdef"), true);
  assert.equal(isSyncTokenShape(" 0123456789abcdef "), true, "前后空白应被 trim");
  assert.equal(isSyncTokenShape("0123456789abcde"), false, "15 字节必须拒");
  assert.equal(isSyncTokenShape("0123456789abcde f"), false, "中间空白必须拒");
  assert.equal(isSyncTokenShape("0123456789abcde\nf"), false, "换行也是空白");
  assert.equal(isSyncTokenShape(""), false);
  // 服务端 cli 侧写进 token 文件的是 trim 后的值；前端提交前也要 trim，
  // 否则桌面复制常带的尾随空格会让下一次启动直接起不来。
  assert.deepEqual(normalizePairingDraft(draft(" https://a ", " tok ", " /ca ")), {
    primaryURL: "https://a",
    token: "tok",
    caFile: "/ca",
  });
});

test("提交载荷只在填了 CA 时才带 ca_file", () => {
  assert.deepEqual(toPairingRequest(draft("https://home:7331", "0123456789abcdef")), {
    primary_url: "https://home:7331",
    token: "0123456789abcdef",
  });
  assert.deepEqual(toPairingRequest(draft("https://home:7331", "0123456789abcdef", "/etc/ca.pem")), {
    primary_url: "https://home:7331",
    token: "0123456789abcdef",
    ca_file: "/etc/ca.pem",
  });
});

test("未配对状态靠只读探针发现，而非 /status", () => {
  // /status 在未配对时同样不注册：前端否则无从区分「等着配对」与「根本不同步」。
  assert.match(service, /controlPath\("\/api\/control-sync\/pair"\)/);
  assert.match(service, /record\.pending_pairing !== true/);
  // 探针 404 与状态端点同一口径：折成 null，不抛给组件。
  const probeBody = service
    .split("export async function fetchControlSyncPairing")[1]
    .split("export async function")[0];
  assert.match(probeBody, /if \(endpointMissing\(err\)\) return null;/);
  // 轮询里 /status 拿回 null 之后才去探配对：两条请求的先后关系就是语义本身。
  const pollBody = hook
    .split("const next = await fetchControlSyncStatus()")[1]
    .split("void load()")[0];
  assert.match(pollBody, /setPairing\(await fetchControlSyncPairing\(\)\)/);
});

test("提交失败按状态码分成可执行的四类", () => {
  // 403 不是故障而是设计：配对改信任锚 + 写密钥 + 改 config，服务端只收本机来源。
  // 经反代从外部打开页面必然撞到它，提示必须指向「去那台机器上打开页面」。
  assert.match(service, /case 403:[\s\S]{0,60}kind: "not-local"/);
  assert.match(service, /case 404:[\s\S]{0,60}kind: "unsupported"/);
  assert.match(service, /case 502:[\s\S]{0,60}kind: "unreachable"/);
  assert.match(service, /kind: "rejected"/);
  // restart_required 缺字段按 true：显示「已连接、无需重启」而实际没生效是最糟方向。
  assert.match(service, /restart_required: record\.restart_required !== false/);
});

test("表单只做展示，提交与状态都在 hook 里", () => {
  // 组件不 import services —— 仓库架构红线，违反会让网络细节散进视图层。
  assert.doesNotMatch(form, /services\/net/);
  assert.match(form, /from "\.\.\/\.\.\/hooks\/useControlSyncStatus"/);
  // 表单本身不轮询：两处各轮询会让「表单说没配上、横幅说在同步」真的发生。
  assert.doesNotMatch(form, /setInterval/);
  // 横幅是唯一持有轮询的地方，未配对时把它整条换成配对入口。
  assert.match(banner, /if \(pairing\?\.pending_pairing\)/);
  assert.match(banner, /<ControlPairingGate/);
});

test("配完不消失：写明要重启才生效", () => {
  // 路由与同步器都在启动期装配，配对只落盘。默默把表单收掉等于让用户
  // 以为「配好了但没变化」。
  assert.match(form, /controlPairing\.restartRequired/);
  assert.match(form, /paired\.restart_required/);
  assert.match(form, /controlPairing\.done/);
});

test("本地校验只在点提交时亮红", () => {
  // 一进面板就说「地址不合法」等于骂用户还没填的输入框。
  assert.match(form, /onClick=\{\(\) => \{[\s\S]{0,220}setLocalIssue\(issue\)/);
  assert.match(form, /if \(issue\) return;/);
  assert.match(form, /role="alert"/);
});

test("中英文都补齐配对文案", () => {
  for (const key of [
    "controlPairing.title",
    "controlPairing.hint",
    "controlPairing.localOnly",
    "controlPairing.primaryURL",
    "controlPairing.primaryURLPlaceholder",
    "controlPairing.token",
    "controlPairing.tokenPlaceholder",
    "controlPairing.tokenHint",
    "controlPairing.caFile",
    "controlPairing.caFilePlaceholder",
    "controlPairing.caFileHint",
    "controlPairing.submit",
    "controlPairing.submitting",
    "controlPairing.open",
    "controlPairing.close",
    "controlPairing.errorUrl",
    "controlPairing.errorScheme",
    "controlPairing.errorToken",
    "controlPairing.errorNotLocal",
    "controlPairing.errorUnsupported",
    "controlPairing.errorUnreachable",
    "controlPairing.errorRejected",
    "controlPairing.done",
    "controlPairing.restartRequired",
    "controlPairing.pendingTitle",
    "controlPairing.pendingHint",
  ]) {
    assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `中文缺 ${key}`);
    assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `英文缺 ${key}`);
  }
  // 两条带插值的文案必须有占位符，否则界面上会出现字面量 {url} / {message}。
  assert.match(zh, /controlPairing\.done": "[^"]*\{url\}[^"]*\{version\}/);
  assert.match(en, /controlPairing\.done": "[^"]*\{url\}[^"]*\{version\}/);
  assert.match(zh, /controlPairing\.errorRejected": "[^"]*\{message\}/);
  assert.match(en, /controlPairing\.errorRejected": "[^"]*\{message\}/);
});

test("源码里用的每个 controlPairing key 都有中英文", () => {
  // 只查清单会漏掉「新写了一行但忘了登记」；这里反向扫源码，按实际用到为准。
  const used = new Set([
    ...[form, banner].flatMap((src) => [...src.matchAll(/t\("(controlPairing\.[A-Za-z]+)"/g)]),
  ].map((m) => m[1]));
  assert.ok(used.size >= 15, `只扫到 ${used.size} 个 key，正则可能失效了`);
  for (const key of used) {
    assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `中文缺 ${key}`);
    assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `英文缺 ${key}`);
  }
  // 反过来：这份断言本身要能失败 —— 共享模块导出的东西真的被用到。
  assert.match(shared, /export function pairingDraftIssue/);
});
