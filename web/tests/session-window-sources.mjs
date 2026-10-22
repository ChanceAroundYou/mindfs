// session-window 三个守卫测试共用的「读源前言」。
//
// 这个分片只为 file-size.md 的硬线（500 行）而存在：逻辑路径解析、剥注释视图、
// Go 包读取、i18n 文案读取在三个文件里逐字相同，复制三份等于给同一处改动留三处
// 要同步的地方。**新增共用读源逻辑写在这里**，三个测试文件只 import。
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { readGoPackage } from "./go-source.mjs";

const sessionServiceDir = path.resolve(import.meta.dirname, "../src/services/session");
const sharedSessionTypesPath = path.resolve(import.meta.dirname, "../src/shared/sessionTypes.ts");
const sessionViewerPath = path.resolve(import.meta.dirname, "../src/components/session/SessionViewer.tsx");
const sessionViewerProjectionPath = path.resolve(import.meta.dirname, "../src/components/session/useSessionViewerExchangeProjection.ts");
const sessionWindowingPath = path.resolve(import.meta.dirname, "../src/components/session/useSessionWindowing.ts");
const targetSeqScrollPath = path.resolve(import.meta.dirname, "../src/components/session/useTargetSeqScroll.ts");
const sessionWindowApplyPath = path.resolve(import.meta.dirname, "../src/components/session/useSessionWindowApply.ts");
const viewerControlsPath = path.resolve(import.meta.dirname, "../src/components/session/SessionViewerFloatingControls.tsx");
const sessionHistoryMenuPath = path.resolve(import.meta.dirname, "../src/components/session/SessionHistoryMenu.tsx");

const sessionSrc = [
  ...fs.readdirSync(sessionServiceDir)
    .filter((name) => name.endsWith(".ts"))
    .map((name) => fs.readFileSync(path.join(sessionServiceDir, name), "utf8")),
  fs.readFileSync(sharedSessionTypesPath, "utf8"),
].join("\n");
const viewerSrc = [sessionViewerPath, sessionViewerProjectionPath, sessionWindowingPath, targetSeqScrollPath, sessionWindowApplyPath, viewerControlsPath, sessionHistoryMenuPath].map((file) => fs.readFileSync(file, "utf8")).join("\n");
// 剥掉注释后的代码视图：注释里提到某个坏写法是正常的（那通常正是在解释它为什么被删）。
const stripComments = (src) => src.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");
const sessionCode = stripComments(sessionSrc);
const viewerCode = stripComments(viewerSrc);

// 淘汰探针：喂 exchanges 进去，拿保留的 exchanges 出来。
function buildEvictFrom(toPersistentSession) {
  return (exchanges) =>
    toPersistentSession({ key: "probe", exchanges, exchange_aux: {} }).exchanges;
}
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 manager.go /
// http.go / http_sessions.go 切成了职责文件（manager_window_reads.go、
// http_sessions_detail.go…），实现没少、只是搬了家。
const managerSrc = readGoPackage("server/internal/session");
const managerWindowSrc = managerSrc;
const httpSessionContract = readGoPackage("server/internal/api");
const httpSessionsSrc = httpSessionContract;

const appDir = path.resolve(import.meta.dirname, "../src/app");
const appSrc = fs.readdirSync(appDir).filter((name) => /\.tsx?$/.test(name)).map((name) => fs.readFileSync(path.join(appDir, name), "utf8")).join("\n");
// 2026-09 App.tsx 拆分：WS 事件处理器整块搬到 app/useRealtimeEvents.ts，契约随文件走。
// done 后的重锚定（F2）与 meta.updated 新 key 的 replace 重拉（F3）都在这批 handler 里，
// 故这几条断言改读新文件；仍在 App 里的（_windowMeta 附加、锚点计数、reportError 冷却、
// SESSION_WINDOW_SIZE）继续读 appSrc。
const realtimeSrc = ["useRealtimeEvents.ts", "realtimeWsHandlers.ts", "realtimeWsSessionMeta.ts"].map((name) => fs.readFileSync(path.resolve(import.meta.dirname, "../src/app", name), "utf8")).join("\n");

// overlay 尾巴派生 + 组合输入
// 判定已搬到纯模块：组件只负责接线，判定本身在 sessionOverlay.ts
const overlaySrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/shared/sessionOverlay.ts"),
  "utf8",
);

const zhSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/zh-CN.ts"), "utf8");
const enSrc = fs.readFileSync(path.resolve(import.meta.dirname, "../src/i18n/locales/en-US.ts"), "utf8");

export {
  sessionSrc,
  viewerSrc,
  sessionCode,
  viewerCode,
  stripComments,
  buildEvictFrom,
  managerSrc,
  managerWindowSrc,
  httpSessionsSrc,
  appSrc,
  realtimeSrc,
  overlaySrc,
  zhSrc,
  enSrc,
};
