/**
 * App.tsx 里不依赖任何组件状态的纯函数。
 *
 * 这些函数只读自己的入参（+ 模块级 import），不闭包任何 ref/state，
 * 所以可以原样搬出来 —— 搬完后 App.tsx 里换成 import，调用点零改动。
 * 没有测试对它们做源码正则断言，因此不需要进 source-map 的 MODULES。
 */

import type { FileEntry } from "../services/prefs/directorySort";

/** 去掉作用域前缀，只留会话/键名本身。 */
export function unscopedRootId(scoped: string): string {
  const k = String(scoped || "");
  const idx = k.lastIndexOf("::");
  return idx >= 0 ? k.slice(idx + 2) : k;
}

/** 目录排序覆盖的键：`rootID:dirPath`（dirPath 为空或等于根时归一成 "."）。 */
export function getDirectorySortKey(
  rootID: string | null | undefined,
  dirPath: string | null | undefined,
): string {
  if (!rootID) {
    return "";
  }
  const normalizedDir = !dirPath || dirPath === rootID ? "." : dirPath;
  return `${rootID}:${normalizedDir}`;
}

/** 后端目录响应归一：没有 entries 就给空数组，调用方不必再判空。 */
export function normalizeTreeResponse(payload: any): { entries: FileEntry[] } {
  if (payload && payload.entries)
    return { entries: payload.entries as FileEntry[] };
  return { entries: [] };
}

/** 可比较的项目根路径：统一分隔符 + 去尾斜杠 + Windows 盘符小写。 */
export function normalizeComparableRootPath(value: string): string {
  let normalized = String(value || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (/^[a-z]:/i.test(normalized)) {
    normalized = normalized.toLowerCase();
  }
  return normalized;
}
