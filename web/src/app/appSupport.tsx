/**
 * appSupport 的过渡门面（2026-09 App.tsx 拆分）。
 *
 * 原本 1204 行的杂物袋已按域拆成五个模块：
 *   appStorage  持久化键与读写助手
 *   appPath     路径规范化与 URL 状态
 *   appSession  会话模型与映射
 *   appTask     任务/看板助手
 *   appMisc     杂项（更新态、插件上下文、根索引、响应式）
 *
 * 本文件暂留 re-export，让 App.tsx 与 8 个 hook 的 import 不必一次全改。
 * 新代码请直接从对应模块导入；本门面后续会随调用点收敛一并删除。
 */
export * from "./appStorage";
export * from "./appPath";
export * from "./appSession";
export * from "./appTask";
export * from "./appMisc";
