// `/relay/<name>/` 是同源只是表象：请求最终落到**另一台机器**的 HTTP 面上，
// 账户表是那台机器自己那份。判成「同源」会把本机账户 id 发过去，对方查无此人，
// 页面打得开但全空。这条规则在 base.ts 的 isSameServerAsPage 里，钉住它。
import assert from "node:assert/strict";
import { afterEach, beforeEach, test } from "vitest";
import { isSameServerAsPage } from "../src/services/net/base.ts";

const PAGE = "https://controller.example/mindfs/";

function stubLocation(href) {
  globalThis.window = { location: { href, origin: new URL(href).origin } };
}

beforeEach(() => stubLocation(PAGE));
afterEach(() => {
  delete globalThis.window;
});

test("relay 路径判为跨机器（不按同源处理）", () => {
  // 带部署前缀，正是主控线上的形状
  assert.equal(isSameServerAsPage("https://controller.example/mindfs/relay/hasee/api/dirs"), false);
  // 不带前缀（本机实例）
  assert.equal(isSameServerAsPage("/relay/hasee/api/dirs"), false);
  // 带查询串与末尾无斜杠都要认出来
  assert.equal(isSameServerAsPage("/relay/pc-win/api/x?root=a"), false);
  // ws 升级同样走这个判断
  assert.equal(isSameServerAsPage("wss://controller.example/mindfs/relay/hasee/ws"), false);
});

test("普通同源路径仍是同源", () => {
  assert.equal(isSameServerAsPage("/api/dirs"), true);
  assert.equal(isSameServerAsPage("https://controller.example/mindfs/api/dirs"), true);
  assert.equal(isSameServerAsPage("wss://controller.example/mindfs/ws"), true);
});

test("文件路径里恰好叫 relay 的目录不算中继入口", () => {
  // 深一层的 relay 是文件路径，不是 /relay/<name> 入口
  assert.equal(isSameServerAsPage("/api/files/relay/hasee.txt"), true);
  assert.equal(isSameServerAsPage("/api/files/mindfs/relay/notes.md"), true);
});

test("relay 段后面没有合法名字时不认作中继", () => {
  assert.equal(isSameServerAsPage("/relay/"), true);
  assert.equal(isSameServerAsPage("/relay"), true);
  // 名字形状不合法（大写 / 下划线 / 超长）不当作边控端
  assert.equal(isSameServerAsPage("/relay/HASEE/x"), true);
  assert.equal(isSameServerAsPage("/relay/ha_see/x"), true);
  assert.equal(isSameServerAsPage("/relay/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/x"), true);
});

test("不同 host 永远不同源", () => {
  assert.equal(isSameServerAsPage("https://other.example/api/dirs"), false);
  assert.equal(isSameServerAsPage("http://controller.example/api/dirs"), false);
});

test("没有 window 时安全返回 false", () => {
  delete globalThis.window;
  assert.equal(isSameServerAsPage("/api/dirs"), false);
});
