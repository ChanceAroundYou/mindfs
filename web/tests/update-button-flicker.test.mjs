import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 底栏「更新」按钮的闪烁回归守卫。
//
// 与 PWA 安装按钮同源的一类问题：按钮的可见性依赖一个**迟到**的状态，而首帧那份
// 状态是「空」的。
//
// updateState 不是启动时同步就绪的，它由 WS 的 app.update 推送（后端
// pushInitialAppUpdate 在每个客户端注册时推一次）。前端初值是
// normalizeUpdateState(null) —— 所有字段 falsy 的「什么都没发生」态。
// 若直接拿它判可见性，首帧按「无更新」画一帧，收到推送后再翻出来，底栏随之长高。
//
// 目前两端 auto_update_supported 均为 false（releaseManifestPublicKey 在
// service.go:33 声明后从未赋值，只在 service_test.go:255 赋过），所以按钮恒不显示、
// 看不到闪烁——这是巧合，不是设计。哪天配上公钥，「先有后无」就会真的发生。
// 这条断言就是在那之前把它钉死。

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const misc = readFileSync(new URL("../src/app/appMisc.tsx", import.meta.url), "utf8");

// 1) 可��性必须以「WS 已连上」为前置门：连上时服务端已把真实状态推来，
//    连上之前那份空初值不代表「无更新」，画出来就是凭空出现。
assert.match(
  app,
  /const showUpdateButton = status === "connected" && shouldShowUpdateButton\(updateState\);/,
  "更新按钮可见性必须以 status === \"connected\" 为前置门（否则首帧空状态会闪一下）",
);

// 2) 门不能加在 FileTree 的 JSX 上：那只是渲染末端，hasFooterContent 已经会跟着
//    变高，那时才藏已经晚了一帧。门必须在状态的所有者（App）这一层。
assert.doesNotMatch(
  app,
  /updateActionLabel=\{showUpdateButton \? updateLabel : null\}[\s\S]{0,200}status === "connected"/,
  "连接态判断不应只塞在 FileTree 的 props 上（应在 showUpdateButton 一处统一）",
);

// 3) 断连不得把按钮翻回去：updateState 在重连时会被服务端重新推送
//    （pushInitialAppUpdate 在每次客户端注册时调用），所以 status 回到 connected
//    时状态是就绪的，不存在「回来后空窗」。
assert.match(
  misc,
  /export function shouldShowUpdateButton\(state: UpdateState\): boolean \{/,
  "更新按钮的判定仍应由 shouldShowUpdateButton 单一出处承担",
);
assert.match(
  misc,
  /state\.auto_update_supported === true && state\.has_update === true/,
  "auto_update_supported 仍是必要条件（当前两端为 false，故按钮恒不显示）",
);
