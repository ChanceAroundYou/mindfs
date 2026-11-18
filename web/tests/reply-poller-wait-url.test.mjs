// 原生 configure 载荷里「长轮询地址」的契约（Task 4：安卓后台服务靠它做近实时提醒）。
//
// 钉住三件事：
//   1) 每个节点同时带 url（普通轮询）与 waitUrl（长轮询），且两条都由 absoluteNodeURL 按
//      同一个 id 算 —— 挂载前缀与 user= 账户参数只有前端知道，原生只 GET；
//   2) 旧锚点 absoluteNodeURL("/api/replying-sessions", id) 仍在（apk-self-update.test.mjs
//      钉的是同一条，删了它降级轮询就没有地址了）；
//   3) 顶层兜底 apiBaseUrl 与 waitUrl 成对：节点表为空时两条一起给，有节点表时两条一起空。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

const LIST_URL_CALL = 'absoluteNodeURL("/api/replying-sessions", id)';
const WAIT_URL_CALL = 'absoluteNodeURL("/api/replying-sessions/wait", id)';

test("每个节点同时下发普通轮询 url 与长轮询 waitUrl", async () => {
  const poller = read("src/services/net/replyPoller.ts");

  // 旧锚点不能被 waitUrl 顶掉。
  assert.ok(poller.includes(LIST_URL_CALL), "普通轮询地址锚点被删了");
  assert.ok(poller.includes(WAIT_URL_CALL), "缺长轮询地址");
  // 同一个 id、同一个 absoluteNodeURL：两条地址必然同源、同账户参数、同挂载前缀。
  assert.match(
    poller,
    /url: absoluteNodeURL\("\/api\/replying-sessions", id\),\s*waitUrl: absoluteNodeURL\("\/api\/replying-sessions\/wait", id\),/,
    "节点对象必须同时带 url 与 waitUrl",
  );
  // 节点仍然只在普通轮询地址可用时才下发（waitUrl 不参与过滤，降级路径不因此少节点）。
  assert.ok(
    poller.includes('.filter((node) => /^https?:\\/\\//i.test(node.url))'),
    "节点过滤仍按普通轮询 url",
  );
});

test("顶层兜底 apiBaseUrl 与 waitUrl 成对下发", async () => {
  const poller = read("src/services/net/replyPoller.ts");

  // 成对：同一个兜底基准，waitUrl 就是它后面挂 /api/replying-sessions/wait；没有基准即空串。
  assert.match(
    poller,
    /const fallbackBaseUrl = nodes\.length \? "" : apiBaseUrl;/,
    "兜底基准只有一个来源",
  );
  assert.match(
    poller,
    /apiBaseUrl: fallbackBaseUrl,\s*waitUrl: fallbackBaseUrl \? `\$\{fallbackBaseUrl\}\/api\/replying-sessions\/wait` : "",/,
    "顶层 waitUrl 必须与 apiBaseUrl 同源成对",
  );
  // 其余契约不变。
  assert.match(poller, /nodes,/, "payload 仍带节点表");
  assert.match(poller, /viewing: viewingKey,/, "viewing 契约不变");
});

test("configure 的 TS 类型声明了 waitUrl（否则 typecheck 与原生载荷不一致）", async () => {
  const poller = read("src/services/net/replyPoller.ts");

  assert.match(poller, /apiBaseUrl\?: string;[\s\S]{0,60}waitUrl\?: string;/, "顶层类型缺 waitUrl");
  assert.match(
    poller,
    /nodes\?: Array<\{ id: string; url: string; waitUrl: string \}>;/,
    "节点类型缺 waitUrl",
  );
});

// ── 原生侧：顶层 waitUrl 必须真的被读走 ────────────────────────────────
// 前端发了顶层 waitUrl，但如果两条原生桥都不转发、服务又自己拼 /wait，这一条就是死载荷。
// 挂载前缀与 user= 账户参数只有前端知道，原生拼出来必然 404 —— 所以两头都钉死。
const SERVICE = "../web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java";
const ACTIVITY = "../web/android/app/src/main/java/com/mindfs/app/MainActivity.java";
const PLUGIN = "../web/android/app/src/main/java/com/mindfs/app/ReplyPollerPlugin.java";

// 按花括号配平取方法正文；被断言的方法正文里没有带花括号的字符串字面量。
function methodBody(source, signature) {
  const start = source.indexOf(signature);
  assert.notEqual(start, -1, `找不到方法签名：${signature}`);
  const open = source.indexOf("{", start);
  assert.notEqual(open, -1, `方法没有正文：${signature}`);
  let depth = 0;
  for (let i = open; i < source.length; i++) {
    if (source[i] === "{") depth++;
    else if (source[i] === "}") {
      depth--;
      if (depth === 0) return source.slice(open, i + 1);
    }
  }
  throw new Error(`方法未闭合：${signature}`);
}

test("顶层 waitUrl 经两条原生桥进入服务，单节点兜底不再自己拼 /wait", async () => {
  const service = read(SERVICE);
  const activity = read(ACTIVITY);
  const plugin = read(PLUGIN);

  // 1) 服务侧常量存在，且键名与前端下发的 "waitUrl" 一致。
  assert.match(service, /static final String EXTRA_WAIT_URL = "waitUrl";/, "服务缺 EXTRA_WAIT_URL 常量");

  // 2) WebView JS 桥：MainActivity.ReplyPollerBridge.configure 必须转发 waitUrl。
  const bridge = methodBody(activity, "public void configure(String rawJSON)");
  assert.match(bridge, /payload\.optString\("waitUrl"/, "WebView 桥丢了顶层 waitUrl");
  assert.match(bridge, /EXTRA_WAIT_URL/, "WebView 桥没把 waitUrl 放进 Intent");

  // 3) Capacitor 插件路径同样转发。
  const pluginConfigure = methodBody(plugin, "public void configure(PluginCall call)");
  assert.match(pluginConfigure, /call\.getString\("waitUrl"/, "Capacitor 插件丢了顶层 waitUrl");
  assert.match(pluginConfigure, /EXTRA_WAIT_URL/, "Capacitor 插件没把 waitUrl 放进 Intent");

  // 4) 单节点兜底必须用收到的整条 waitUrl；不许回到 url + "/wait" 的字符串拼接。
  const targets = methodBody(service, "private List<NodeTarget> nodeTargets()");
  assert.match(targets, /new NodeTarget\("", url, resolveWaitUrl\(url, waitUrl\)\)/, "兜底没用下发的 waitUrl");
  assert.doesNotMatch(targets, /new NodeTarget\("", url, url \+ "\/wait"\)/, "兜底又回到自己拼 /wait");
});
