import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

/**
 * 2026-09：弹窗曾经「点了确认也不消失」——服务只 resolve 了 promise，
 * 没把 null 推给宿主，宿主本地 state 于是永远停在旧请求上。
 * 源码文本断言抓不到这种回归，这里真跑一遍 dialog 服务的生命周期。
 */

const sourcePath = path.resolve(import.meta.dirname, "../src/services/dialog.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const { dialogService, confirmDialog, promptDialog, alertDialog } = sandbox.exports;

// 模拟 DialogHost：订阅并记录每次推送
let visible = null;
const seen = [];
dialogService.subscribe((request) => {
  visible = request;
  seen.push(request);
});

function settleMicrotasks() {
  return new Promise((resolve) => setImmediate(resolve));
}

// --- 1) 确认：确认后必须消失，且调用点拿到 true ---
const confirmed = confirmDialog({ message: "删除项目？" });
assert.equal(visible?.kind, "confirm", "confirmDialog 应立刻把弹窗推给宿主");
assert.equal(visible?.message, "删除项目？");
assert.equal(dialogService.current?.kind, "confirm");

dialogService.resolve(true);
assert.equal(await confirmed, true, "resolve(true) 应让 await 拿到 true");
assert.equal(visible, null, "确认后宿主必须收到 null（弹窗消失）—— 这条就是回归点");
assert.equal(dialogService.current, null);

// --- 2) 确认：取消走 dismiss，拿到 false ---
const cancelled = confirmDialog({ message: "真的删？" });
assert.equal(visible?.kind, "confirm");
dialogService.dismiss();
assert.equal(await cancelled, false, "dismiss 应让 await 拿到 false");
assert.equal(visible, null, "取消后弹窗也必须消失");

// --- 3) 提示（alert）：不返回 promise，但关闭后必须消失 ---
alertDialog("操作失败");
assert.equal(visible?.kind, "alert");
dialogService.resolve(true);
assert.equal(visible, null, "alert 关闭后必须消失");

// --- 4) 输入：回填值 / 取消给 null ---
const prompted = promptDialog({ message: "原因？", defaultValue: "预填" });
assert.equal(visible?.kind, "prompt");
assert.equal(visible?.defaultValue, "预填");
dialogService.resolve("用户填的原因");
assert.equal(await prompted, "用户填的原因", "prompt 应回填用户输入");
assert.equal(visible, null);

const promptCancelled = promptDialog({ message: "原因？" });
dialogService.dismiss();
assert.equal(await promptCancelled, null, "取消输入应得到 null（与 window.prompt 一致）");
assert.equal(visible, null);

// --- 5) 覆盖：后来的弹窗顶掉前一个时，前一个不能永远挂着 ---
let firstSettled = null;
const first = confirmDialog({ message: "第一个" }).then((v) => {
  firstSettled = v;
  return v;
});
confirmDialog({ message: "第二个" });
await settleMicrotasks();
assert.equal(firstSettled, false, "被覆盖的弹窗应按取消结算，否则调用点会永远 await");
assert.equal(visible?.message, "第二个", "新弹窗应成为当前可见的");
dialogService.dismiss();

// --- 6) 空闲时重复关闭不应报错 / 不应多推 ---
const before = seen.length;
dialogService.dismiss();
dialogService.resolve(true);
assert.equal(seen.length, before, "没有弹窗时 resolve/dismiss 不应产生额外推送");

console.log("dialog lifecycle OK");
