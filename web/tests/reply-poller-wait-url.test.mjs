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
